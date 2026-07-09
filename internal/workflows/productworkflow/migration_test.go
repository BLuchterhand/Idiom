package productworkflow

import (
	"reflect"
	"strings"
	"testing"
)

func TestProductStatusMigrationPlansStartedToClosedWithoutMutation(t *testing.T) {
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

	if plan.Status != "planned" || len(plan.Blockers) != 0 {
		t.Fatalf("expected planned migration without blockers, got %#v", plan)
	}

	if plan.EffortID != "test-effort" || plan.SourceStatusBucket != "started" || plan.TargetStatusBucket != "closed" {
		t.Fatalf("unexpected identity/buckets: %#v", plan)
	}

	if plan.SourceFolderPath != "started/test-effort" || plan.TargetFolderPath != "closed/test-effort" {
		t.Fatalf("unexpected folder paths: %#v", plan)
	}

	if plan.UpdatedWorkflowState.StatusBucket != "closed" || plan.UpdatedWorkflowState.CurrentState != "closed" {
		t.Fatalf("expected closed workflow state, got %#v", plan.UpdatedWorkflowState)
	}

	if plan.UpdatedWorkflowState.StateRevision != ws.StateRevision+1 || plan.UpdatedWorkflowState.LastEventID != "evt-migration-001" {
		t.Fatalf("expected revision and event update, got %#v", plan.UpdatedWorkflowState)
	}

	if plan.UpdatedWorkflowState.FolderPath != "docs/product-workflows/efforts/closed/test-effort" {
		t.Fatalf("unexpected workflow folder_path: %q", plan.UpdatedWorkflowState.FolderPath)
	}

	if got := plan.UpdatedWorkflowState.PriorFolderPaths; len(got) != 1 || got[0] != "docs/product-workflows/efforts/started/test-effort" {
		t.Fatalf("expected prior workflow folder path, got %#v", got)
	}

	if !reflect.DeepEqual(plan.UpdatedWorkflowState.NextValidActions, []string{"inspect_closed_effort"}) {
		t.Fatalf("closed target should have a terminal inspect action, got %#v", plan.UpdatedWorkflowState.NextValidActions)
	}

	if plan.UpdatedIndexEntry.FolderPath != "closed/test-effort" || plan.UpdatedIndexEntry.StatusBucket != "closed" || plan.UpdatedIndexEntry.CurrentState != "closed" {
		t.Fatalf("unexpected updated index entry: %#v", plan.UpdatedIndexEntry)
	}

	if plan.UpdatedEffortIndex.UpdatedAt != "2026-05-18T21:40:00Z" {
		t.Fatalf("expected updated effort index timestamp, got %q", plan.UpdatedEffortIndex.UpdatedAt)
	}

	if len(plan.UpdatedEffortIndex.Efforts) != 1 || !reflect.DeepEqual(plan.UpdatedEffortIndex.Efforts[0], plan.UpdatedIndexEntry) {
		t.Fatalf("expected full updated effort index payload, got %#v", plan.UpdatedEffortIndex)
	}

	if got := plan.UpdatedIndexEntry.PriorPaths; len(got) != 1 || got[0] != "started/test-effort" {
		t.Fatalf("expected prior index path, got %#v", got)
	}

	if got := plan.PriorFolderPaths; len(got) != 1 || got[0] != "docs/product-workflows/efforts/started/test-effort" {
		t.Fatalf("expected top-level workflow prior folder paths, got %#v", got)
	}

	if got := plan.PriorIndexPaths; len(got) != 1 || got[0] != "started/test-effort" {
		t.Fatalf("expected top-level index prior paths, got %#v", got)
	}

	if plan.Event.Event != "status_migration_planned" ||
		plan.Event.EventID != "evt-migration-001" ||
		plan.Event.EffortID != "test-effort" ||
		plan.Event.TaskID != "impl-test" ||
		plan.Event.Timestamp != "2026-05-18T21:40:00Z" ||
		plan.Event.Source != "product_status_migration_planner" ||
		plan.Event.Details.FromFolderPath != "started/test-effort" ||
		plan.Event.Details.ToFolderPath != "closed/test-effort" ||
		plan.Event.Details.StateRevision != ws.StateRevision+1 {

		t.Fatalf("unexpected migration event: %#v", plan.Event)
	}

	if !reflect.DeepEqual(ws, beforeWS) || !reflect.DeepEqual(idx, beforeIndex) {
		t.Fatalf("planner mutated inputs")
	}
}

func TestProductStatusMigrationPlansUnstartedToStartedWithPriorPaths(t *testing.T) {
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

	if plan.Status != "planned" {
		t.Fatalf("expected planned migration, got %#v", plan)
	}

	wantWorkflowPriors := []string{
		"docs/product-workflows/efforts/unstarted/legacy-test-effort",
		"docs/product-workflows/efforts/unstarted/test-effort",
	}
	if !reflect.DeepEqual(plan.UpdatedWorkflowState.PriorFolderPaths, wantWorkflowPriors) {
		t.Fatalf("unexpected workflow prior paths: %#v", plan.UpdatedWorkflowState.PriorFolderPaths)
	}

	if !reflect.DeepEqual(plan.PriorFolderPaths, wantWorkflowPriors) {
		t.Fatalf("unexpected top-level workflow prior paths: %#v", plan.PriorFolderPaths)
	}

	wantIndexPriors := []string{"unstarted/legacy-test-effort", "unstarted/test-effort"}
	if !reflect.DeepEqual(plan.UpdatedIndexEntry.PriorPaths, wantIndexPriors) {
		t.Fatalf("unexpected index prior paths: %#v", plan.UpdatedIndexEntry.PriorPaths)
	}

	if !reflect.DeepEqual(plan.PriorIndexPaths, wantIndexPriors) {
		t.Fatalf("unexpected top-level index prior paths: %#v", plan.PriorIndexPaths)
	}

	if !reflect.DeepEqual(plan.UpdatedWorkflowState.NextValidActions, []string{"submit_for_human_verification", "start_investigation"}) {
		t.Fatalf("unexpected started/planned next valid actions: %#v", plan.UpdatedWorkflowState.NextValidActions)
	}
}

func TestProductStatusMigrationFailsClosedForInvalidInputs(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			plan := PlanProductStatusMigration(tc.ws, tc.idx, tc.opts)
			for _, want := range tc.want {
				if !migrationTestHasBlocker(plan, want) {
					t.Fatalf("expected blocker %s, got %#v", want, plan.Blockers)
				}
			}
		})
	}

	dup := migrationTestEffortIndex(ws)

	dup.Efforts = append(dup.Efforts, dup.Efforts[0])

	plan := PlanProductStatusMigration(ws, dup, ProductStatusMigrationOptions{SourceFolderPath: "started/test-effort", TargetStatusBucket: "closed"})

	if !migrationTestHasBlocker(plan, "duplicate_effort_index_entry") {
		t.Fatalf("expected duplicate index blocker, got %#v", plan.Blockers)
	}
}

func TestProductStatusMigrationPlanDoesNotLeakAbsolutePaths(t *testing.T) {
	ws := migrationTestWorkflowState("started", "reviewing")
	idx := migrationTestEffortIndex(ws)

	plan := PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
		SourceFolderPath:   "started/test-effort",
		TargetStatusBucket: "closed",
		UpdatedAt:          "2026-05-18T21:40:00Z",
		MigrationEventID:   "evt-migration-003",
	})

	if plan.Status != "planned" {
		t.Fatalf("expected planned migration, got %#v", plan)
	}

	for _, value := range []string{plan.SourceFolderPath, plan.TargetFolderPath, plan.UpdatedIndexEntry.FolderPath, plan.Event.Details.FromFolderPath, plan.Event.Details.ToFolderPath} {
		if len(value) > 0 && value[0] == '/' {
			t.Fatalf("plan leaked absolute path %q", value)
		}
	}
}

func TestProductStatusMigrationRejectsAbsoluteOptionPaths(t *testing.T) {
	ws := migrationTestWorkflowState("started", "reviewing")
	idx := migrationTestEffortIndex(ws)

	plan := PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
		SourceFolderPath:   "/started/test-effort",
		TargetStatusBucket: "closed",
		UpdatedAt:          "2026-05-18T21:40:00Z",
		MigrationEventID:   "evt-migration-absolute-source",
	})
	if !migrationTestHasBlocker(plan, "unsafe_source_folder_path") {
		t.Fatalf("expected unsafe_source_folder_path blocker, got %#v", plan.Blockers)
	}

	plan = PlanProductStatusMigration(ws, idx, ProductStatusMigrationOptions{
		SourceFolderPath:   "started/test-effort",
		TargetStatusBucket: "closed",
		UpdatedAt:          "2026-05-18T21:40:00Z",
		MigrationEventID:   "evt-migration-absolute-root",
		EffortsRoot:        "/docs/product-workflows/efforts",
	})
	if !migrationTestHasBlocker(plan, "unsafe_efforts_root") {
		t.Fatalf("expected unsafe_efforts_root blocker, got %#v", plan.Blockers)
	}
}

func TestProductStatusMigrationRejectsAndRedactsPersistedAbsolutePaths(t *testing.T) {
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
		if !migrationTestHasBlocker(plan, want) {
			t.Fatalf("expected blocker %s, got %#v", want, plan.Blockers)
		}
	}

	if strings.Contains(migrationTestBlockerText(plan), "/workspace/idiom") {
		t.Fatalf("blockers leaked raw absolute path: %#v", plan.Blockers)
	}

	if len(plan.UpdatedWorkflowState.PriorFolderPaths) != 0 {
		t.Fatalf("blocked plan should not carry unsafe workflow prior paths, got %#v", plan.UpdatedWorkflowState.PriorFolderPaths)
	}
}

func TestProductStatusMigrationParsersRejectMalformedJSON(t *testing.T) {
	if _, err := ParseEffortIndexJSON([]byte(`{not-json`)); err == nil {
		t.Fatal("expected malformed effort index JSON to fail")
	}

	if _, err := ParseWorkflowStateJSON([]byte(`{not-json`)); err == nil {
		t.Fatal("expected malformed workflow state JSON to fail")
	}
}

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
