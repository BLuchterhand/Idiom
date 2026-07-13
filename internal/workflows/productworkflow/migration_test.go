package productworkflow

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("migration", func() {
	ginkgo.It("ProductStatusMigrationPlansStartedToClosedWithoutMutation", func() {
		ws := migrationTestWorkflowState("started", "reviewing")
		idx := migrationTestEffortIndex(ws)
		beforeWS := ws
		beforeIndex := idx

		plan := PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
			SourceFolderPath:     "started/test-effort",
			TargetStatusBucket:   "closed",
			UpdatedAt:            "2026-05-18T21:40:00Z",
			MigrationEventID:     "evt-migration-001",
			WorkspaceFingerprint: "idiom@test",
			EffortsRoot:          "docs/product-workflows/efforts",
		})
		gomega.Expect(plan.Status).To(gomega.Equal(ProductWorkflowStatusPlanned), fmt.Sprintf("expected planned migration without blockers, got %#v", plan))
		gomega.Expect(plan.Blockers).To(gomega.BeEmpty(), fmt.Sprintf("expected planned migration without blockers, got %#v", plan))
		gomega.Expect(plan.EffortID).To(gomega.Equal("test-effort"), fmt.Sprintf("unexpected identity/buckets: %#v", plan))
		gomega.Expect(plan.SourceStatusBucket).To(gomega.Equal("started"), fmt.Sprintf("unexpected identity/buckets: %#v", plan))
		gomega.Expect(plan.TargetStatusBucket).To(gomega.Equal("closed"), fmt.Sprintf("unexpected identity/buckets: %#v", plan))
		gomega.Expect(plan.SourceFolderPath).To(gomega.Equal("started/test-effort"), fmt.Sprintf("unexpected folder paths: %#v", plan))
		gomega.Expect(plan.TargetFolderPath).To(gomega.Equal("closed/test-effort"), fmt.Sprintf("unexpected folder paths: %#v", plan))
		gomega.Expect(plan.UpdatedWorkflowState.StatusBucket).To(gomega.Equal("closed"), fmt.Sprintf("expected closed workflow state, got %#v", plan.UpdatedWorkflowState))
		gomega.Expect(plan.UpdatedWorkflowState.CurrentState).To(gomega.Equal("closed"), fmt.Sprintf("expected closed workflow state, got %#v", plan.UpdatedWorkflowState))
		gomega.Expect(plan.UpdatedWorkflowState.StateRevision).To(gomega.Equal(ws.StateRevision+1), fmt.Sprintf("expected revision and event update, got %#v", plan.UpdatedWorkflowState))
		gomega.Expect(plan.UpdatedWorkflowState.LastEventID).To(gomega.Equal("evt-migration-001"), fmt.Sprintf("expected revision and event update, got %#v", plan.UpdatedWorkflowState))
		gomega.Expect(plan.UpdatedWorkflowState.FolderPath != "docs/product-workflows/efforts/closed/test-effort").To(gomega.BeFalse(), fmt.Sprintf("unexpected workflow folder_path: %q", plan.UpdatedWorkflowState.FolderPath))
		{

			got := plan.UpdatedWorkflowState.PriorFolderPaths
			gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("expected prior workflow folder path, got %#v", got))
			gomega.Expect(got[0]).To(gomega.Equal("docs/product-workflows/efforts/started/test-effort"), fmt.Sprintf("expected prior workflow folder path, got %#v", got))
		}
		gomega.Expect(!reflect.DeepEqual(plan.UpdatedWorkflowState.NextValidActions, []string{"inspect_closed_effort"})).To(gomega.BeFalse(), fmt.Sprintf("closed target should have a terminal inspect action, got %#v", plan.UpdatedWorkflowState.NextValidActions))
		gomega.Expect(plan.UpdatedIndexEntry.FolderPath).To(gomega.Equal("closed/test-effort"), fmt.Sprintf("unexpected updated index entry: %#v", plan.UpdatedIndexEntry))
		gomega.Expect(plan.UpdatedIndexEntry.StatusBucket).To(gomega.Equal("closed"), fmt.Sprintf("unexpected updated index entry: %#v", plan.UpdatedIndexEntry))
		gomega.Expect(plan.UpdatedIndexEntry.CurrentState).To(gomega.Equal("closed"), fmt.Sprintf("unexpected updated index entry: %#v", plan.UpdatedIndexEntry))
		gomega.Expect(plan.UpdatedEffortIndex.UpdatedAt != "2026-05-18T21:40:00Z").To(gomega.BeFalse(), fmt.Sprintf("expected updated effort index timestamp, got %q", plan.UpdatedEffortIndex.UpdatedAt))
		gomega.Expect(plan.UpdatedEffortIndex.Efforts).To(gomega.HaveLen(1), fmt.Sprintf("expected full updated effort index payload, got %#v", plan.UpdatedEffortIndex))
		gomega.Expect(reflect.DeepEqual(plan.UpdatedEffortIndex.Efforts[0], plan.UpdatedIndexEntry)).To(gomega.BeTrue(), fmt.Sprintf("expected full updated effort index payload, got %#v", plan.UpdatedEffortIndex))
		{

			got := plan.UpdatedIndexEntry.PriorPaths
			gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("expected prior index path, got %#v", got))
			gomega.Expect(got[0]).To(gomega.Equal("started/test-effort"), fmt.Sprintf("expected prior index path, got %#v", got))
		}
		{

			got := plan.PriorFolderPaths
			gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("expected top-level workflow prior folder paths, got %#v", got))
			gomega.Expect(got[0]).To(gomega.Equal("docs/product-workflows/efforts/started/test-effort"), fmt.Sprintf("expected top-level workflow prior folder paths, got %#v", got))
		}
		{

			got := plan.PriorIndexPaths
			gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("expected top-level index prior paths, got %#v", got))
			gomega.Expect(got[0]).To(gomega.Equal("started/test-effort"), fmt.Sprintf("expected top-level index prior paths, got %#v", got))
		}
		gomega.Expect(plan.Event.Event).To(gomega.Equal("status_migration_planned"), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(plan.Event.EventID).To(gomega.Equal("evt-migration-001"), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(plan.Event.EffortID).To(gomega.Equal("test-effort"), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(plan.Event.TaskID).To(gomega.Equal("impl-test"), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(plan.Event.Timestamp).To(gomega.Equal("2026-05-18T21:40:00Z"), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(plan.Event.Source).To(gomega.Equal("product_status_migration_planner"), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(plan.Event.Details.FromFolderPath).To(gomega.Equal("started/test-effort"), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(plan.Event.Details.ToFolderPath).To(gomega.Equal("closed/test-effort"), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(plan.Event.Details.StateRevision).To(gomega.Equal(ws.StateRevision+1), fmt.Sprintf("unexpected migration event: %#v", plan.Event))
		gomega.Expect(reflect.DeepEqual(ws, beforeWS)).To(gomega.BeTrue(), fmt.Sprintf("planner mutated inputs"))
		gomega.Expect(reflect.DeepEqual(idx, beforeIndex)).To(gomega.BeTrue(), fmt.Sprintf("planner mutated inputs"))

	})
	ginkgo.It("ProductStatusMigrationPlansUnstartedToStartedWithPriorPaths", func() {
		ws := migrationTestWorkflowState("unstarted", "planned")

		ws.PriorFolderPaths = []string{"docs/product-workflows/efforts/unstarted/legacy-test-effort"}

		idx := migrationTestEffortIndex(ws)

		idx.Efforts[0].PriorPaths = []string{"unstarted/legacy-test-effort"}

		plan := PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
			SourceFolderPath:   "unstarted/test-effort",
			TargetStatusBucket: "started",
			UpdatedAt:          "2026-05-18T21:40:00Z",
			MigrationEventID:   "evt-migration-002",
		})
		gomega.Expect(plan.Status != "planned").To(gomega.BeFalse(), fmt.Sprintf("expected planned migration, got %#v", plan))

		wantWorkflowPriors := []string{
			"docs/product-workflows/efforts/unstarted/legacy-test-effort",
			"docs/product-workflows/efforts/unstarted/test-effort",
		}
		gomega.Expect(!reflect.DeepEqual(plan.UpdatedWorkflowState.PriorFolderPaths, wantWorkflowPriors)).To(gomega.BeFalse(), fmt.Sprintf("unexpected workflow prior paths: %#v", plan.UpdatedWorkflowState.PriorFolderPaths))
		gomega.Expect(!reflect.DeepEqual(plan.PriorFolderPaths, wantWorkflowPriors)).To(gomega.BeFalse(), fmt.Sprintf("unexpected top-level workflow prior paths: %#v", plan.PriorFolderPaths))

		wantIndexPriors := []string{"unstarted/legacy-test-effort", "unstarted/test-effort"}
		gomega.Expect(!reflect.DeepEqual(plan.UpdatedIndexEntry.PriorPaths, wantIndexPriors)).To(gomega.BeFalse(), fmt.Sprintf("unexpected index prior paths: %#v", plan.UpdatedIndexEntry.PriorPaths))
		gomega.Expect(!reflect.DeepEqual(plan.PriorIndexPaths, wantIndexPriors)).To(gomega.BeFalse(), fmt.Sprintf("unexpected top-level index prior paths: %#v", plan.PriorIndexPaths))
		gomega.Expect(!reflect.DeepEqual(plan.UpdatedWorkflowState.NextValidActions, []string{"submit_for_human_verification", "start_investigation"})).To(gomega.BeFalse(), fmt.Sprintf("unexpected started/planned next valid actions: %#v", plan.UpdatedWorkflowState.NextValidActions))

	})
	ginkgo.It("ProductStatusMigrationFailsClosedForInvalidInputs", func() {
		ws := migrationTestWorkflowState("started", "implementing")
		idx := migrationTestEffortIndex(ws)

		cases := []struct {
			name string
			ws   WorkflowState
			idx  EffortIndex
			opts ProductStatusMigrationOptions
			want []string
		}{
			{"invalid target", ws, idx, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "later"}, []string{"invalid_target_status_bucket"}},
			{"noop", ws, idx, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "started"}, []string{"noop_status_migration"}},
			{"missing timestamp", ws, idx, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "closed", MigrationEventID: "evt-migration"}, []string{"missing_migration_timestamp"}},
			{"missing event id", ws, idx, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "closed", UpdatedAt: "2026-05-18T21:40:00Z"}, []string{"missing_migration_event_id"}},
			{"started to unstarted", ws, idx, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "unstarted", UpdatedAt: "2026-05-18T21:40:00Z", MigrationEventID: "evt-migration"}, []string{"invalid_status_migration"}},
			{"closed to started", migrationTestWorkflowState("closed", "closed"), migrationTestEffortIndex(migrationTestWorkflowState("closed", "closed")), ProductStatusMigrationOptions{SourceFolderPath: "closed/test-effort", TargetStatusBucket: "started", UpdatedAt: "2026-05-18T21:40:00Z", MigrationEventID: "evt-migration"}, []string{"invalid_status_migration"}},
			{"bad state", migrationTestWorkflowState("started", "closed"), idx, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "closed"}, []string{"invalid_state_bucket"}},
			{"unsafe source", ws, idx, ProductStatusMigrationOptions{SourceFolderPath: "../started/test-effort", TargetStatusBucket: "closed"}, []string{"unsafe_source_folder_path"}},
			{"mismatched identity", ws, idx, ProductStatusMigrationOptions{SourceFolderPath: "started/other-effort", TargetStatusBucket: "closed"}, []string{"workflow_folder_path_mismatch", "effort_id_folder_mismatch", "index_folder_path_mismatch"}},
			{"missing index", ws, EffortIndex{SchemaVersion: "test", UpdatedAt: "now"}, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "closed"}, []string{"missing_effort_index_entry"}},
		}
		for _, tc := range cases {
			ginkgo.By(tc.name, func() {
				plan := PlanProductStatusMigration(tc.ws, tc.idx, tc.opts)
				for _, want := range tc.want {
					gomega.Expect(!migrationTestHasBlocker(plan, want)).To(gomega.BeFalse(), fmt.Sprintf("expected blocker %s, got %#v", want, plan.Blockers))

				}
			})
		}

		dup := migrationTestEffortIndex(ws)

		dup.Efforts = append(dup.Efforts, dup.Efforts[0])

		plan := PlanProductStatusMigration(ws, dup, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "closed"})
		gomega.Expect(!migrationTestHasBlocker(plan, "duplicate_effort_index_entry")).To(gomega.BeFalse(), fmt.Sprintf("expected duplicate index blocker, got %#v", plan.Blockers))

	})
	ginkgo.It("ProductStatusMigrationPlanDoesNotLeakAbsolutePaths", func() {
		ws := migrationTestWorkflowState("started", "reviewing")
		idx := migrationTestEffortIndex(ws)

		plan := PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
			SourceFolderPath:   "started/test-effort",
			TargetStatusBucket: "closed",
			UpdatedAt:          "2026-05-18T21:40:00Z",
			MigrationEventID:   "evt-migration-003",
		})
		gomega.Expect(plan.Status != "planned").To(gomega.BeFalse(), fmt.Sprintf("expected planned migration, got %#v", plan))

		for _, value := range []string{plan.SourceFolderPath, plan.TargetFolderPath, plan.UpdatedIndexEntry.FolderPath, plan.Event.Details.FromFolderPath, plan.Event.Details.ToFolderPath} {
			gomega.Expect(filepath.IsAbs(value)).To(gomega.BeFalse(), fmt.Sprintf("plan leaked absolute path %q", value))

		}
	})
	ginkgo.It("ProductStatusMigrationRejectsAbsoluteOptionPaths", func() {
		ws := migrationTestWorkflowState("started", "reviewing")
		idx := migrationTestEffortIndex(ws)

		plan := PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
			SourceFolderPath:   "/started/test-effort",
			TargetStatusBucket: "closed",
			UpdatedAt:          "2026-05-18T21:40:00Z",
			MigrationEventID:   "evt-migration-absolute-source",
		})
		gomega.Expect(!migrationTestHasBlocker(plan, "unsafe_source_folder_path")).To(gomega.BeFalse(), fmt.Sprintf("expected unsafe_source_folder_path blocker, got %#v", plan.Blockers))

		plan = PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
			SourceFolderPath:   "started/test-effort",
			TargetStatusBucket: "closed",
			UpdatedAt:          "2026-05-18T21:40:00Z",
			MigrationEventID:   "evt-migration-absolute-root",
			EffortsRoot:        "/docs/product-workflows/efforts",
		})
		gomega.Expect(!migrationTestHasBlocker(plan, "unsafe_efforts_root")).To(gomega.BeFalse(), fmt.Sprintf("expected unsafe_efforts_root blocker, got %#v", plan.Blockers))

	})
	ginkgo.It("ProductStatusMigrationRejectsAndRedactsPersistedAbsolutePaths", func() {
		ws := migrationTestWorkflowState("started", "reviewing")

		ws.FolderPath = "/workspace/idiom/docs/product-workflows/efforts/started/test-effort"
		ws.PriorFolderPaths = []string{"/workspace/idiom/docs/product-workflows/efforts/started/old-test-effort"}

		idx := migrationTestEffortIndex(migrationTestWorkflowState("started", "reviewing"))

		idx.Efforts[0].FolderPath = "/workspace/idiom/docs/product-workflows/efforts/started/test-effort"
		idx.Efforts[0].PriorPaths = []string{"/workspace/idiom/docs/product-workflows/efforts/started/old-test-effort"}

		plan := PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
			SourceFolderPath:   "started/test-effort",
			TargetStatusBucket: "closed",
			UpdatedAt:          "2026-05-18T21:40:00Z",
			MigrationEventID:   "evt-migration-absolute-persisted",
		})

		for _, want := range []string{
			"unsafe_workflow_folder_path",
			"unsafe_workflow_prior_folder_path",
			"unsafe_index_folder_path",
			"unsafe_index_prior_path",
		} {
			gomega.Expect(!migrationTestHasBlocker(plan, want)).To(gomega.BeFalse(), fmt.Sprintf("expected blocker %s, got %#v", want, plan.Blockers))

		}
		gomega.Expect(strings.Contains(migrationTestBlockerText(plan), "/workspace/idiom")).To(gomega.BeFalse(), fmt.Sprintf("blockers leaked raw absolute path: %#v", plan.Blockers))
		gomega.Expect(len(plan.UpdatedWorkflowState.PriorFolderPaths) != 0).To(gomega.BeFalse(), fmt.Sprintf("blocked plan should not carry unsafe workflow prior paths, got %#v", plan.UpdatedWorkflowState.PriorFolderPaths))

	})
	ginkgo.It("ProductStatusMigrationParsersRejectMalformedJSON", func() {
		{
			_, err := ParseEffortIndexJSON([]byte(`{not-json`))
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected malformed effort index JSON to fail"))
		}
		{

			_, err := ParseWorkflowStateJSON([]byte(`{not-json`))
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected malformed workflow state JSON to fail"))
		}

	})
})

func migrationTestWorkflowState(bucket, state string) WorkflowState {
	return WorkflowState{
		SchemaVersion:        "idiom.product_workflow_state.v0.fixture",
		EffortID:             "test-effort",
		TaskID:               "impl-test",
		Title:                "Test Effort",
		CreatedAt:            "2026-05-18T21:00:00Z",
		UpdatedAt:            "2026-05-18T21:00:00Z",
		StateRevision:        7,
		LastEventID:          "evt-007",
		WorkspaceFingerprint: "idiom@old",
		StatusBucket:         bucket,
		CurrentPhase:         "phase-test",
		CurrentState:         state,
		ArtifactPaths:        map[string]string{"events": "events.jsonl"},
		Context:              WorkflowContext{ContextPackID: "pack", FreshnessState: "current"},
		Coverage:             CoverageState{CoverageTier: "unit"},
		Privacy:              PrivacyState{RedactionStatus: "applied"},
		FolderPath:           "docs/product-workflows/efforts/" + bucket + "/test-effort",
		NextValidActions:     []string{"complete"},
		DurabilityLevel:      "source_controlled",
	}
}

func migrationTestEffortIndex(ws WorkflowState) EffortIndex {
	return EffortIndex{
		SchemaVersion: "idiom.effort_index.v1",
		UpdatedAt:     "2026-05-18T21:00:00Z",
		Efforts: []EffortIndexEntry{{
			EffortID:     ws.EffortID,
			Title:        ws.Title,
			StatusBucket: ws.StatusBucket,
			CurrentPhase: ws.CurrentPhase,
			CurrentState: ws.CurrentState,
			FolderPath:   ws.StatusBucket + "/" + ws.EffortID,
			TaskID:       ws.TaskID,
			CreatedAt:    ws.CreatedAt,
			PriorPaths:   []string{},
		}},
	}
}

func migrationTestHasBlocker(plan ProductStatusMigrationPlan, code string) bool {
	for _, blocker := range plan.Blockers {
		if blocker.Code == code {
			return true
		}
	}

	return false
}

func migrationTestBlockerText(plan ProductStatusMigrationPlan) string {
	var out strings.Builder

	for _, blocker := range plan.Blockers {
		out.WriteString(blocker.Path)
		out.WriteString(" ")
		out.WriteString(blocker.Message)
		out.WriteString("\n")
	}

	return out.String()
}
