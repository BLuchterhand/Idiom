package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("migration apply", func() {
	ginkgo.It("ProductStatusMigrationApplyMovesFolderAndWritesStateEventIndex", func() {
		fixture := newMigrationApplyFixture()

		result := ApplyProductStatusMigration(ProductStatusMigrationApplyOptions{
			EffortRoot:                 fixture.sourceRoot,
			EffortsRoot:                fixture.effortsRoot,
			TargetStatusBucket:         "closed",
			UpdatedAt:                  "2026-05-18T22:10:00Z",
			MigrationEventID:           "evt-migration-apply-001",
			WorkspaceFingerprint:       "idiom@apply",
			ExpectedStateRevision:      7,
			ExpectedSourceStatusBucket: "started",
		})
		gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusApplied), fmt.Sprintf("expected applied result without blockers, got %#v", result))
		gomega.Expect(result.Blockers).To(gomega.BeEmpty(), fmt.Sprintf("expected applied result without blockers, got %#v", result))
		gomega.Expect(result.Event.Event).To(gomega.Equal("status_migration_applied"), fmt.Sprintf("unexpected apply event %#v", result.Event))
		gomega.Expect(result.Event.Source).To(gomega.Equal("product_status_migration_apply"), fmt.Sprintf("unexpected apply event %#v", result.Event))
		{

			_, err := os.Stat(fixture.sourceRoot)
			gomega.Expect(!os.IsNotExist(err)).To(gomega.BeFalse(), fmt.Sprintf("expected source folder to move away, got err=%v", err))
		}
		{

			_, err := os.Stat(filepath.Join(fixture.targetRoot, "README.md"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected README.md to move with effort folder: %v", err))
		}
		{

			_, err := os.Stat(filepath.Join(fixture.targetRoot, "briefs", "test.md"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected relative artifact paths to remain intact after move: %v", err))
		}

		workflowBytes, err := os.ReadFile(filepath.Join(fixture.targetRoot, "workflow_state.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read migrated workflow_state.json: %v", err))

		workflowState, err := ParseWorkflowStateJSON(workflowBytes)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse migrated workflow_state.json: %v", err))
		gomega.Expect(workflowState.EffortID != "test-effort").To(gomega.BeFalse(), fmt.Sprintf("expected stable effort_id, got %#v", workflowState))
		gomega.Expect(workflowState.StatusBucket).To(gomega.Equal("closed"), fmt.Sprintf("expected closed workflow state, got %#v", workflowState))
		gomega.Expect(workflowState.CurrentState).To(gomega.Equal("closed"), fmt.Sprintf("expected closed workflow state, got %#v", workflowState))
		gomega.Expect(workflowState.StateRevision).To(gomega.Equal(8), fmt.Sprintf("expected revision and event update, got %#v", workflowState))
		gomega.Expect(workflowState.LastEventID).To(gomega.Equal("evt-migration-apply-001"), fmt.Sprintf("expected revision and event update, got %#v", workflowState))
		gomega.Expect(workflowState.FolderPath != "docs/product-workflows/efforts/closed/test-effort").To(gomega.BeFalse(), fmt.Sprintf("unexpected migrated workflow folder_path %q", workflowState.FolderPath))
		gomega.Expect(!reflect.DeepEqual(workflowState.PriorFolderPaths, []string{"docs/product-workflows/efforts/started/test-effort"})).To(gomega.BeFalse(), fmt.Sprintf("unexpected workflow prior paths %#v", workflowState.PriorFolderPaths))

		eventsBytes, err := os.ReadFile(filepath.Join(fixture.targetRoot, "events.jsonl"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read migrated events.jsonl: %v", err))

		lines := strings.Split(strings.TrimSpace(string(eventsBytes)), "\n")
		gomega.Expect(len(lines) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected 2 events after append, got %d", len(lines)))

		var appliedEvent ProductStatusMigrationEvent
		{

			err := json.Unmarshal([]byte(lines[1]), &appliedEvent)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse appended event: %v", err))
		}
		gomega.Expect(appliedEvent.Event).To(gomega.Equal("status_migration_applied"), fmt.Sprintf("unexpected appended event %#v", appliedEvent))
		gomega.Expect(appliedEvent.EventID).To(gomega.Equal("evt-migration-apply-001"), fmt.Sprintf("unexpected appended event %#v", appliedEvent))

		indexBytes, err := os.ReadFile(filepath.Join(fixture.effortsRoot, "index.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read updated effort index: %v", err))

		index, err := ParseEffortIndexJSON(indexBytes)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse updated effort index: %v", err))

		entry, blockers := migrationIndexEntry(index, "test-effort")
		gomega.Expect(len(blockers)).NotTo(gomega.BeNumerically(">", 0), fmt.Sprintf("expected migrated effort index entry, blockers=%#v", blockers))
		gomega.Expect(entry).NotTo(gomega.BeNil(), fmt.Sprintf("expected migrated effort index entry, blockers=%#v", blockers))
		gomega.Expect(entry.StatusBucket).To(gomega.Equal("closed"), fmt.Sprintf("unexpected migrated index entry %#v", entry))
		gomega.Expect(entry.FolderPath).To(gomega.Equal("closed/test-effort"), fmt.Sprintf("unexpected migrated index entry %#v", entry))
		gomega.Expect(!reflect.DeepEqual(entry.PriorPaths, []string{"started/test-effort"})).To(gomega.BeFalse(), fmt.Sprintf("unexpected index prior paths %#v", entry.PriorPaths))
		gomega.Expect(index.UpdatedAt != "2026-05-18T22:10:00Z").To(gomega.BeFalse(), fmt.Sprintf("unexpected index updated_at %q", index.UpdatedAt))

	})
	ginkgo.It("ProductStatusMigrationApplyFailsClosedWithoutPartialMutation", func() {
		cases := []struct {
			name    string
			mutate  func(fixture *migrationApplyFixture)
			options func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions
			want    []string
		}{
			{
				name: "stale revision",
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					return fixture.options("closed")
				},
				mutate: func(fixture *migrationApplyFixture) {},
				want:   []string{"stale_workflow_state_revision"},
			},
			{
				name:   "missing expected revision",
				mutate: func(fixture *migrationApplyFixture) {},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = -1

					return opts
				},
				want: []string{"missing_expected_revision"},
			},
			{
				name: "existing target folder",
				mutate: func(fixture *migrationApplyFixture) {
					{
						err := os.MkdirAll(fixture.targetRoot, 0o755)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir target root: %v", err))
					}

				},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = 7

					return opts
				},
				want: []string{"target_folder_exists"},
			},
			{
				name: "missing source folder",
				mutate: func(fixture *migrationApplyFixture) {
					{
						err := os.RemoveAll(fixture.sourceRoot)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("remove source root: %v", err))
					}

				},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = 7

					return opts
				},
				want: []string{"product_workflow_lock_error"},
			},
			{
				name: "cross root effort path",
				mutate: func(fixture *migrationApplyFixture) {
					otherRoot := filepath.Join(fixture.root, "other-efforts")
					{
						err := os.MkdirAll(otherRoot, 0o755)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir other efforts root: %v", err))
					}

					indexBytes, err := os.ReadFile(filepath.Join(fixture.effortsRoot, "index.json"))
					gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read index: %v", err))
					{

						err := os.WriteFile(filepath.Join(otherRoot, "index.json"), indexBytes, 0o644)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write other index: %v", err))
					}

					fixture.otherEffortsRoot = otherRoot
				},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = 7
					opts.EffortsRoot = filepath.Join(fixture.root, "other-efforts")

					return opts
				},
				want: []string{"unsafe_source_folder_path"},
			},
			{
				name: "symlinked effort root",
				mutate: func(fixture *migrationApplyFixture) {
					externalRoot := filepath.Join(fixture.root, "external-effort")
					{
						err := os.Rename(fixture.sourceRoot, externalRoot)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("move source root outside efforts tree: %v", err))
					}
					{

						err := os.MkdirAll(filepath.Dir(fixture.sourceRoot), 0o755)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("restore source parent: %v", err))
					}

					if err := os.Symlink(externalRoot, fixture.sourceRoot); err != nil {
						ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
					}
				},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = 7

					return opts
				},
				want: []string{"product_workflow_lock_error"},
			},
			{
				name: "symlinked events file",
				mutate: func(fixture *migrationApplyFixture) {
					eventsPath := filepath.Join(fixture.sourceRoot, "events.jsonl")
					externalEventsPath := filepath.Join(fixture.root, "external-events.jsonl")

					eventsBytes, err := os.ReadFile(eventsPath)
					gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read events file: %v", err))
					{

						err := os.WriteFile(externalEventsPath, eventsBytes, 0o644)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write external events file: %v", err))
					}
					{

						err := os.Remove(eventsPath)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("remove events file: %v", err))
					}

					if err := os.Symlink(externalEventsPath, eventsPath); err != nil {
						ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
					}
				},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = 7

					return opts
				},
				want: []string{"read_events_file_error"},
			},
			{
				name: "mismatched index workflow state",
				mutate: func(fixture *migrationApplyFixture) {
					index := fixture.index

					index.Efforts[0].StatusBucket = "unstarted"
					index.Efforts[0].CurrentState = "planned"
					writeMigrationApplyJSON(filepath.Join(fixture.effortsRoot, "index.json"), index)
				},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = 7

					return opts
				},
				want: []string{"index_status_bucket_mismatch"},
			},
			{
				name: "malformed workflow state",
				mutate: func(fixture *migrationApplyFixture) {
					writeMigrationApplyText(filepath.Join(fixture.sourceRoot, "workflow_state.json"), `{not json`)
				},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = 7

					return opts
				},
				want: []string{"malformed_workflow_state"},
			},
			{
				name: "malformed events jsonl",
				mutate: func(fixture *migrationApplyFixture) {
					writeMigrationApplyText(filepath.Join(fixture.sourceRoot, "events.jsonl"), "{bad json")
				},
				options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
					opts := fixture.options("closed")

					opts.ExpectedStateRevision = 7

					return opts
				},
				want: []string{"malformed_events_jsonl"},
			},
		}

		for _, tc := range cases {
			ginkgo.By(tc.name, func() {
				fixture := newMigrationApplyFixture()
				tc.mutate(fixture)

				before := snapshotMigrationApplyTree(fixture.root)

				result := ApplyProductStatusMigration(tc.options(fixture))
				gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked result, got %#v", result))

				for _, want := range tc.want {
					gomega.Expect(!migrationTestHasBlocker(result, want)).To(gomega.BeFalse(), fmt.Sprintf("expected blocker %q, got %#v", want, result.Blockers))

				}

				after := snapshotMigrationApplyTree(fixture.root)
				gomega.Expect(!reflect.DeepEqual(before, after)).To(gomega.BeFalse(), fmt.Sprintf("expected fail-closed no-partial-mutation snapshot, before=%#v after=%#v", before, after))

			})
		}
	})
	ginkgo.It("ProductStatusMigrationApplyRejectsSymlinkedEffortRoot", func() {
		fixture := newMigrationApplyFixture()
		externalRoot := filepath.Join(fixture.root, "external-effort")
		{

			err := os.Rename(fixture.sourceRoot, externalRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("move fixture source to external root: %v", err))
		}

		if err := os.Symlink(externalRoot, fixture.sourceRoot); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		externalBefore := snapshotMigrationApplyTree(externalRoot)

		opts := fixture.options("closed")

		opts.ExpectedStateRevision = 7

		result := ApplyProductStatusMigration(opts)
		gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusBlocked), fmt.Sprintf("expected product_workflow_lock_error blocker, got %#v", result))
		gomega.Expect(migrationTestHasBlocker(result, "product_workflow_lock_error")).To(gomega.BeTrue(), fmt.Sprintf("expected product_workflow_lock_error blocker, got %#v", result))
		gomega.Expect(result.Blockers).To(gomega.ContainElement(gomega.HaveField("Message", gomega.ContainSubstring("effort root must not be a symlink"))))
		{

			linkTarget, err := os.Readlink(fixture.sourceRoot)
			gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected source symlink to remain untouched, target=%q err=%v", linkTarget, err))
			gomega.Expect(linkTarget).To(gomega.Equal(externalRoot), fmt.Sprintf("expected source symlink to remain untouched, target=%q err=%v", linkTarget, err))
		}
		{

			_, err := os.Stat(fixture.targetRoot)
			gomega.Expect(!os.IsNotExist(err)).To(gomega.BeFalse(), fmt.Sprintf("expected target folder not to be created, stat err=%v", err))
		}

		externalAfter := snapshotMigrationApplyTree(externalRoot)
		gomega.Expect(!reflect.DeepEqual(externalBefore, externalAfter)).To(gomega.BeFalse(), fmt.Sprintf("expected symlinked external effort to remain unchanged, before=%#v after=%#v", externalBefore, externalAfter))

	})
	ginkgo.It("ProductStatusMigrationApplyRejectsSymlinkedJournalCandidatesWithoutPartialMutation", func() {
		cases := []struct {
			name          string
			candidateName string
		}{
			{
				name:          "workflow candidate symlink",
				candidateName: productStatusMigrationCandidateWorkflowName,
			},
			{
				name:          "events candidate symlink",
				candidateName: productStatusMigrationCandidateEventsName,
			},
			{
				name:          "index candidate symlink",
				candidateName: productStatusMigrationCandidateIndexName,
			},
		}

		for _, tc := range cases {
			ginkgo.By(tc.name, func() {
				fixture := newMigrationApplyFixture()
				prepareMigrationApplyJournal(fixture)
				externalCandidate := filepath.Join(fixture.root, strings.ReplaceAll(tc.name, " ", "-")+".external")
				writeMigrationApplyText(externalCandidate, "external-original\n")

				candidatePath := filepath.Join(
					fixture.effortsRoot,
					productStatusMigrationTransactionRelPath,
					tc.candidateName,
				)
				gomega.Expect(os.Remove(candidatePath)).To(gomega.Succeed())

				if err := os.Symlink(externalCandidate, candidatePath); err != nil {
					ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
				}

				before := snapshotMigrationApplyTree(fixture.root)

				opts := fixture.options("closed")

				opts.ExpectedStateRevision = 7

				result := ApplyProductStatusMigration(opts)
				gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusBlocked), fmt.Sprintf("expected recovery blocker for %s, got %#v", tc.name, result))
				gomega.Expect(migrationTestHasBlocker(result, "status_migration_recovery_error")).To(gomega.BeTrue(), fmt.Sprintf("expected status_migration_recovery_error blocker, got %#v", result))
				gomega.Expect(result.Blockers).To(gomega.ContainElement(gomega.HaveField("Message", gomega.ContainSubstring("must be a regular file and must not be a symlink"))))

				after := snapshotMigrationApplyTree(fixture.root)
				gomega.Expect(!reflect.DeepEqual(before, after)).To(gomega.BeFalse(), fmt.Sprintf("expected journal candidate symlink rejection without partial mutation, before=%#v after=%#v", before, after))

			})
		}
	})
	ginkgo.It("ProductStatusMigrationApplyRecoversForwardAfterFolderRenameInterruption", func() {
		fixture := newMigrationApplyFixture()
		originalHook := productStatusMigrationAfterFolderRenameHook

		productStatusMigrationAfterFolderRenameHook = func() error {
			return os.ErrPermission
		}
		ginkgo.DeferCleanup(func() {
			productStatusMigrationAfterFolderRenameHook = originalHook
		})

		opts := fixture.options("closed")

		opts.ExpectedStateRevision = 7

		interrupted := ApplyProductStatusMigration(opts)
		productStatusMigrationAfterFolderRenameHook = originalHook

		expectProductStatusMigrationForwardRecovery(fixture, opts, interrupted)

	})
	ginkgo.It("ProductStatusMigrationApplyRecoversForwardAfterReplacementSyncInterruption", func() {
		fixture := newMigrationApplyFixture()
		originalSyncFile := productStatusMigrationSyncFile

		productStatusMigrationSyncFile = func(path string) error {
			if strings.HasSuffix(filepath.ToSlash(path), "closed/test-effort/workflow_state.json") {
				return os.ErrPermission
			}

			return originalSyncFile(path)
		}
		ginkgo.DeferCleanup(func() {
			productStatusMigrationSyncFile = originalSyncFile
		})

		opts := fixture.options("closed")

		opts.ExpectedStateRevision = 7

		interrupted := ApplyProductStatusMigration(opts)
		productStatusMigrationSyncFile = originalSyncFile

		expectProductStatusMigrationForwardRecovery(fixture, opts, interrupted)

	})
})

func prepareMigrationApplyJournal(fixture *migrationApplyFixture) {
	ginkgo.GinkgoHelper()

	options := fixture.options("closed")
	plan := PlanProductStatusMigrationFromEffortRoot(options)
	gomega.Expect(plan.Status).To(gomega.Equal(ProductWorkflowStatusPlanned), fmt.Sprintf("prepare migration journal plan: %#v", plan))
	gomega.Expect(plan.Blockers).To(gomega.BeEmpty())
	plan.Event.Event = "status_migration_applied"
	plan.Event.Source = "product_status_migration_apply"
	eventsPath, err := productWorkflowEventsPath(fixture.workflowState)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	previousWorkflow, err := os.ReadFile(filepath.Join(fixture.sourceRoot, WorkflowStateFileName))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	previousEvents, err := os.ReadFile(filepath.Join(fixture.sourceRoot, eventsPath))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	previousIndex, err := os.ReadFile(filepath.Join(fixture.effortsRoot, "index.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	candidateWorkflow, err := marshalIndentedJSON(plan.UpdatedWorkflowState)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	candidateEvents, err := appendProductStatusMigrationEvent(previousEvents, plan.Event)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	candidateIndex, err := marshalIndentedJSON(plan.UpdatedEffortIndex)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

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
		EventsPath:              filepath.ToSlash(eventsPath),
		EffortID:                plan.EffortID,
		EventID:                 plan.Event.EventID,
		PreviousWorkflowDigest:  productStatusMigrationDigest(previousWorkflow),
		CandidateWorkflowDigest: productStatusMigrationDigest(candidateWorkflow),
		PreviousEventsDigest:    productStatusMigrationDigest(previousEvents),
		CandidateEventsDigest:   productStatusMigrationDigest(candidateEvents),
		PreviousIndexDigest:     productStatusMigrationDigest(previousIndex),
		CandidateIndexDigest:    productStatusMigrationDigest(candidateIndex),
	}

	effortsRoot, err := os.OpenRoot(fixture.effortsRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer effortsRoot.Close()
	gomega.Expect(prepareProductStatusMigration(effortsRoot, manifest, candidates)).To(gomega.Succeed())
}

func expectProductStatusMigrationForwardRecovery(
	fixture *migrationApplyFixture,
	options ProductStatusMigrationApplyOptions,
	interrupted ProductStatusMigrationPlan,
) {
	ginkgo.GinkgoHelper()

	gomega.Expect(interrupted.Status).To(gomega.Equal(ProductWorkflowStatusApplied), fmt.Sprintf("expected committed migration interruption, got %#v", interrupted))
	gomega.Expect(interrupted.CommitWarning).To(gomega.ContainSubstring("forward recovery required"))
	_, err := os.Stat(fixture.sourceRoot)
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("expected committed source rename, stat err=%v", err))
	_, err = os.Stat(fixture.targetRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected committed target rename: %v", err))
	_, err = os.Stat(filepath.Join(fixture.effortsRoot, productStatusMigrationTransactionRelPath))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected retained recovery journal: %v", err))
	_, err = os.Stat(filepath.Join(fixture.targetRoot, productStatusMigrationPendingMarkerName))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected retained migration fence: %v", err))

	recovered := ApplyProductStatusMigration(options)
	gomega.Expect(recovered.Status).To(gomega.Equal(ProductWorkflowStatusApplied), fmt.Sprintf("expected interrupted migration recovery, got %#v", recovered))
	gomega.Expect(recovered.Blockers).To(gomega.BeEmpty())
	gomega.Expect(recovered.CommitWarning).To(gomega.ContainSubstring("recovered an interrupted product status migration"))
	_, err = os.Stat(filepath.Join(fixture.effortsRoot, productStatusMigrationTransactionRelPath))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("expected recovery journal cleanup, stat err=%v", err))
	_, err = os.Stat(filepath.Join(fixture.targetRoot, productStatusMigrationPendingMarkerName))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("expected migration fence cleanup, stat err=%v", err))

	workflowBody, err := os.ReadFile(filepath.Join(fixture.targetRoot, WorkflowStateFileName))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	workflow, err := ParseWorkflowStateJSON(workflowBody)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(workflow.StatusBucket).To(gomega.Equal(WorkflowStatusBucketClosed))
	gomega.Expect(workflow.StateRevision).To(gomega.Equal(8))
	gomega.Expect(workflow.LastEventID).To(gomega.Equal(options.MigrationEventID))
	eventsPath, err := productWorkflowEventsPath(workflow)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	indexBody, err := os.ReadFile(filepath.Join(fixture.effortsRoot, "index.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	index, err := ParseEffortIndexJSON(indexBody)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	entry, blockers := migrationIndexEntry(index, fixture.workflowState.EffortID)
	gomega.Expect(blockers).To(gomega.BeEmpty())
	gomega.Expect(entry).NotTo(gomega.BeNil())
	gomega.Expect(entry.StatusBucket).To(gomega.Equal(WorkflowStatusBucketClosed))

	eventsBody, err := os.ReadFile(filepath.Join(fixture.targetRoot, eventsPath))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	_, err = productStatusMigrationEventByID(eventsBody, options.MigrationEventID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

type migrationApplyFixture struct {
	root             string
	effortsRoot      string
	sourceRoot       string
	targetRoot       string
	otherEffortsRoot string
	workflowState    WorkflowState
	index            EffortIndex
}

func newMigrationApplyFixture() *migrationApplyFixture {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	sourceRoot := filepath.Join(effortsRoot, "started", "test-effort")
	targetRoot := filepath.Join(effortsRoot, "closed", "test-effort")
	workflowState := migrationTestWorkflowState("started", "reviewing")
	index := migrationTestEffortIndex(workflowState)

	writeMigrationApplyJSON(filepath.Join(effortsRoot, "index.json"), index)
	writeMigrationApplyJSON(filepath.Join(sourceRoot, "workflow_state.json"), workflowState)
	writeMigrationApplyText(filepath.Join(sourceRoot, "events.jsonl"), `{"event":"workflow_state_updated","event_id":"evt-007","effort_id":"test-effort","task_id":"impl-test","timestamp":"2026-05-18T21:00:00Z","source":"fixture","details":{"state_revision":7}}`+"\n")
	writeMigrationApplyText(filepath.Join(sourceRoot, "README.md"), "see briefs/test.md\n")
	writeMigrationApplyText(filepath.Join(sourceRoot, "briefs", "test.md"), "# Test Brief\n")

	return &migrationApplyFixture{
		root:          root,
		effortsRoot:   effortsRoot,
		sourceRoot:    sourceRoot,
		targetRoot:    targetRoot,
		workflowState: workflowState,
		index:         index,
	}
}

func (fixture *migrationApplyFixture) options(targetStatus string) ProductStatusMigrationApplyOptions {
	return ProductStatusMigrationApplyOptions{
		EffortRoot:                 fixture.sourceRoot,
		EffortsRoot:                fixture.effortsRoot,
		TargetStatusBucket:         targetStatus,
		UpdatedAt:                  "2026-05-18T22:10:00Z",
		MigrationEventID:           "evt-migration-apply-001",
		WorkspaceFingerprint:       "idiom@apply",
		ExpectedStateRevision:      6,
		ExpectedSourceStatusBucket: "started",
	}
}

func snapshotMigrationApplyTree(root string) map[string]string {
	ginkgo.GinkgoHelper()

	snapshot := map[string]string{}
	{

		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			switch filepath.Base(rel) {
			case productStatusMigrationIndexLockRelPath, productWorkflowMutationLockRelPath:
				return nil
			}

			if d.Type()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}

				snapshot[filepath.ToSlash(rel)] = "SYMLINK->" + target

				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			snapshot[filepath.ToSlash(rel)] = string(data)

			return nil
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("snapshot tree %s: %v", root, err))
	}

	return snapshot
}

func writeMigrationApplyJSON(path string, value any) {
	ginkgo.GinkgoHelper()

	data, err := marshalIndentedJSON(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal JSON for %s: %v", path, err))

	writeMigrationApplyText(path, string(data))
}

func writeMigrationApplyText(path string, content string) {
	ginkgo.GinkgoHelper()
	{

		err := os.MkdirAll(filepath.Dir(path), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", filepath.Dir(path), err))
	}
	{

		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
	}

}
