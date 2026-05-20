package productworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProductStatusMigrationApplyMovesFolderAndWritesStateEventIndex(t *testing.T) {
	fixture := newMigrationApplyFixture(t)

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
	if result.Status != "applied" || len(result.Blockers) != 0 {
		t.Fatalf("expected applied result without blockers, got %#v", result)
	}
	if result.Event.Event != "status_migration_applied" || result.Event.Source != "product_status_migration_apply" {
		t.Fatalf("unexpected apply event %#v", result.Event)
	}

	if _, err := os.Stat(fixture.sourceRoot); !os.IsNotExist(err) {
		t.Fatalf("expected source folder to move away, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.targetRoot, "README.md")); err != nil {
		t.Fatalf("expected README.md to move with effort folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.targetRoot, "briefs", "test.md")); err != nil {
		t.Fatalf("expected relative artifact paths to remain intact after move: %v", err)
	}

	workflowBytes, err := os.ReadFile(filepath.Join(fixture.targetRoot, "workflow_state.json"))
	if err != nil {
		t.Fatalf("read migrated workflow_state.json: %v", err)
	}
	workflowState, err := ParseWorkflowStateJSON(workflowBytes)
	if err != nil {
		t.Fatalf("parse migrated workflow_state.json: %v", err)
	}
	if workflowState.EffortID != "test-effort" {
		t.Fatalf("expected stable effort_id, got %#v", workflowState)
	}
	if workflowState.StatusBucket != "closed" || workflowState.CurrentState != "closed" {
		t.Fatalf("expected closed workflow state, got %#v", workflowState)
	}
	if workflowState.StateRevision != 8 || workflowState.LastEventID != "evt-migration-apply-001" {
		t.Fatalf("expected revision and event update, got %#v", workflowState)
	}
	if workflowState.FolderPath != "docs/product-workflows/efforts/closed/test-effort" {
		t.Fatalf("unexpected migrated workflow folder_path %q", workflowState.FolderPath)
	}
	if !reflect.DeepEqual(workflowState.PriorFolderPaths, []string{"docs/product-workflows/efforts/started/test-effort"}) {
		t.Fatalf("unexpected workflow prior paths %#v", workflowState.PriorFolderPaths)
	}

	eventsBytes, err := os.ReadFile(filepath.Join(fixture.targetRoot, "events.jsonl"))
	if err != nil {
		t.Fatalf("read migrated events.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(eventsBytes)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 events after append, got %d", len(lines))
	}
	var appliedEvent ProductStatusMigrationEvent
	if err := json.Unmarshal([]byte(lines[1]), &appliedEvent); err != nil {
		t.Fatalf("parse appended event: %v", err)
	}
	if appliedEvent.Event != "status_migration_applied" || appliedEvent.EventID != "evt-migration-apply-001" {
		t.Fatalf("unexpected appended event %#v", appliedEvent)
	}

	indexBytes, err := os.ReadFile(filepath.Join(fixture.effortsRoot, "index.json"))
	if err != nil {
		t.Fatalf("read updated effort index: %v", err)
	}
	index, err := ParseEffortIndexJSON(indexBytes)
	if err != nil {
		t.Fatalf("parse updated effort index: %v", err)
	}
	entry, blockers := migrationIndexEntry(index, "test-effort")
	if len(blockers) > 0 || entry == nil {
		t.Fatalf("expected migrated effort index entry, blockers=%#v", blockers)
	}
	if entry.StatusBucket != "closed" || entry.FolderPath != "closed/test-effort" {
		t.Fatalf("unexpected migrated index entry %#v", entry)
	}
	if !reflect.DeepEqual(entry.PriorPaths, []string{"started/test-effort"}) {
		t.Fatalf("unexpected index prior paths %#v", entry.PriorPaths)
	}
	if index.UpdatedAt != "2026-05-18T22:10:00Z" {
		t.Fatalf("unexpected index updated_at %q", index.UpdatedAt)
	}
}

func TestProductStatusMigrationApplyFailsClosedWithoutPartialMutation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, fixture *migrationApplyFixture)
		options func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions
		want    []string
	}{
		{
			name: "stale revision",
			options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
				return fixture.options("closed")
			},
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {},
			want:   []string{"stale_workflow_state_revision"},
		},
		{
			name:   "missing expected revision",
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {},
			options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
				opts := fixture.options("closed")
				opts.ExpectedStateRevision = -1
				return opts
			},
			want: []string{"missing_expected_revision"},
		},
		{
			name: "existing target folder",
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {
				if err := os.MkdirAll(fixture.targetRoot, 0o755); err != nil {
					t.Fatalf("mkdir target root: %v", err)
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
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {
				if err := os.RemoveAll(fixture.sourceRoot); err != nil {
					t.Fatalf("remove source root: %v", err)
				}
			},
			options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
				opts := fixture.options("closed")
				opts.ExpectedStateRevision = 7
				return opts
			},
			want: []string{"missing_source_folder", "missing_workflow_state"},
		},
		{
			name: "cross root effort path",
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {
				otherRoot := filepath.Join(fixture.root, "other-efforts")
				if err := os.MkdirAll(otherRoot, 0o755); err != nil {
					t.Fatalf("mkdir other efforts root: %v", err)
				}
				indexBytes, err := os.ReadFile(filepath.Join(fixture.effortsRoot, "index.json"))
				if err != nil {
					t.Fatalf("read index: %v", err)
				}
				if err := os.WriteFile(filepath.Join(otherRoot, "index.json"), indexBytes, 0o644); err != nil {
					t.Fatalf("write other index: %v", err)
				}
				fixture.otherEffortsRoot = otherRoot
			},
			options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
				opts := fixture.options("closed")
				opts.ExpectedStateRevision = 7
				opts.EffortsRoot = filepath.Join(fixture.root, "other-efforts")
				return opts
			},
			want: []string{"effort_root_outside_efforts_root"},
		},
		{
			name: "symlinked effort root",
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {
				externalRoot := filepath.Join(fixture.root, "external-effort")
				if err := os.Rename(fixture.sourceRoot, externalRoot); err != nil {
					t.Fatalf("move source root outside efforts tree: %v", err)
				}
				if err := os.MkdirAll(filepath.Dir(fixture.sourceRoot), 0o755); err != nil {
					t.Fatalf("restore source parent: %v", err)
				}
				if err := os.Symlink(externalRoot, fixture.sourceRoot); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
				opts := fixture.options("closed")
				opts.ExpectedStateRevision = 7
				return opts
			},
			want: []string{"unsafe_effort_root_symlink"},
		},
		{
			name: "symlinked events file",
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {
				eventsPath := filepath.Join(fixture.sourceRoot, "events.jsonl")
				externalEventsPath := filepath.Join(fixture.root, "external-events.jsonl")
				eventsBytes, err := os.ReadFile(eventsPath)
				if err != nil {
					t.Fatalf("read events file: %v", err)
				}
				if err := os.WriteFile(externalEventsPath, eventsBytes, 0o644); err != nil {
					t.Fatalf("write external events file: %v", err)
				}
				if err := os.Remove(eventsPath); err != nil {
					t.Fatalf("remove events file: %v", err)
				}
				if err := os.Symlink(externalEventsPath, eventsPath); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			options: func(fixture *migrationApplyFixture) ProductStatusMigrationApplyOptions {
				opts := fixture.options("closed")
				opts.ExpectedStateRevision = 7
				return opts
			},
			want: []string{"unsafe_events_file_symlink"},
		},
		{
			name: "mismatched index workflow state",
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {
				index := fixture.index
				index.Efforts[0].StatusBucket = "unstarted"
				writeMigrationApplyJSON(t, filepath.Join(fixture.effortsRoot, "index.json"), index)
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
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {
				writeMigrationApplyText(t, filepath.Join(fixture.sourceRoot, "workflow_state.json"), `{not json`)
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
			mutate: func(t *testing.T, fixture *migrationApplyFixture) {
				writeMigrationApplyText(t, filepath.Join(fixture.sourceRoot, "events.jsonl"), "{bad json")
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
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMigrationApplyFixture(t)
			tc.mutate(t, fixture)
			before := snapshotMigrationApplyTree(t, fixture.root)

			result := ApplyProductStatusMigration(tc.options(fixture))
			if result.Status != "blocked" {
				t.Fatalf("expected blocked result, got %#v", result)
			}
			for _, want := range tc.want {
				if !migrationTestHasBlocker(result, want) {
					t.Fatalf("expected blocker %q, got %#v", want, result.Blockers)
				}
			}

			after := snapshotMigrationApplyTree(t, fixture.root)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("expected fail-closed no-partial-mutation snapshot, before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestProductStatusMigrationApplyRejectsSymlinkedEffortRoot(t *testing.T) {
	fixture := newMigrationApplyFixture(t)
	externalRoot := filepath.Join(fixture.root, "external-effort")
	if err := os.Rename(fixture.sourceRoot, externalRoot); err != nil {
		t.Fatalf("move fixture source to external root: %v", err)
	}
	if err := os.Symlink(externalRoot, fixture.sourceRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	externalBefore := snapshotMigrationApplyTree(t, externalRoot)

	opts := fixture.options("closed")
	opts.ExpectedStateRevision = 7
	result := ApplyProductStatusMigration(opts)
	if result.Status != "blocked" || !migrationTestHasBlocker(result, "unsafe_effort_root_symlink") {
		t.Fatalf("expected unsafe_effort_root_symlink blocker, got %#v", result)
	}
	if linkTarget, err := os.Readlink(fixture.sourceRoot); err != nil || linkTarget != externalRoot {
		t.Fatalf("expected source symlink to remain untouched, target=%q err=%v", linkTarget, err)
	}
	if _, err := os.Stat(fixture.targetRoot); !os.IsNotExist(err) {
		t.Fatalf("expected target folder not to be created, stat err=%v", err)
	}
	externalAfter := snapshotMigrationApplyTree(t, externalRoot)
	if !reflect.DeepEqual(externalBefore, externalAfter) {
		t.Fatalf("expected symlinked external effort to remain unchanged, before=%#v after=%#v", externalBefore, externalAfter)
	}
}

func TestProductStatusMigrationApplyRejectsPreExistingTempSymlinksWithoutPartialMutation(t *testing.T) {
	cases := []struct {
		name     string
		tempPath func(fixture *migrationApplyFixture) string
		blocker  string
	}{
		{
			name: "workflow temp symlink",
			tempPath: func(fixture *migrationApplyFixture) string {
				return filepath.Join(fixture.sourceRoot, "workflow_state.json.status-migration.tmp")
			},
			blocker: "unsafe_workflow_state_temp_symlink",
		},
		{
			name: "events temp symlink",
			tempPath: func(fixture *migrationApplyFixture) string {
				return filepath.Join(fixture.sourceRoot, "events.jsonl.status-migration.tmp")
			},
			blocker: "unsafe_events_temp_symlink",
		},
		{
			name: "index temp symlink",
			tempPath: func(fixture *migrationApplyFixture) string {
				return filepath.Join(fixture.effortsRoot, "index.json.status-migration.tmp")
			},
			blocker: "unsafe_effort_index_temp_symlink",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMigrationApplyFixture(t)
			externalTemp := filepath.Join(fixture.root, strings.ReplaceAll(tc.name, " ", "-")+".external")
			writeMigrationApplyText(t, externalTemp, "external-original\n")
			if err := os.Symlink(externalTemp, tc.tempPath(fixture)); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			before := snapshotMigrationApplyTree(t, fixture.root)

			opts := fixture.options("closed")
			opts.ExpectedStateRevision = 7
			result := ApplyProductStatusMigration(opts)
			if result.Status != "blocked" || !migrationTestHasBlocker(result, tc.blocker) {
				t.Fatalf("expected %s blocker, got %#v", tc.blocker, result)
			}

			after := snapshotMigrationApplyTree(t, fixture.root)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("expected temp symlink rejection without partial mutation, before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestProductStatusMigrationApplyRollsBackAfterFolderRenameFailure(t *testing.T) {
	fixture := newMigrationApplyFixture(t)
	before := snapshotMigrationApplyTree(t, fixture.root)
	originalHook := productStatusMigrationAfterFolderRenameHook
	productStatusMigrationAfterFolderRenameHook = func() error {
		return os.ErrPermission
	}
	t.Cleanup(func() {
		productStatusMigrationAfterFolderRenameHook = originalHook
	})

	opts := fixture.options("closed")
	opts.ExpectedStateRevision = 7
	result := ApplyProductStatusMigration(opts)
	if result.Status != "blocked" || !migrationTestHasBlocker(result, "status_migration_apply_error") {
		t.Fatalf("expected rollback-backed apply error, got %#v", result)
	}
	if _, err := os.Stat(fixture.sourceRoot); err != nil {
		t.Fatalf("expected source effort restored after rollback: %v", err)
	}
	if _, err := os.Stat(fixture.targetRoot); !os.IsNotExist(err) {
		t.Fatalf("expected target effort removed after rollback, stat err=%v", err)
	}
	after := snapshotMigrationApplyTree(t, fixture.root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("expected rollback to restore tree, before=%#v after=%#v", before, after)
	}
}

func TestProductStatusMigrationApplyRollsBackAfterReplacementSyncFailure(t *testing.T) {
	fixture := newMigrationApplyFixture(t)
	before := snapshotMigrationApplyTree(t, fixture.root)
	originalSyncFile := productStatusMigrationSyncFile
	productStatusMigrationSyncFile = func(path string) error {
		if strings.HasSuffix(filepath.ToSlash(path), "closed/test-effort/workflow_state.json") {
			return os.ErrPermission
		}
		return originalSyncFile(path)
	}
	t.Cleanup(func() {
		productStatusMigrationSyncFile = originalSyncFile
	})

	opts := fixture.options("closed")
	opts.ExpectedStateRevision = 7
	result := ApplyProductStatusMigration(opts)
	if result.Status != "blocked" || !migrationTestHasBlocker(result, "status_migration_apply_error") {
		t.Fatalf("expected rollback-backed sync error, got %#v", result)
	}
	if _, err := os.Stat(fixture.sourceRoot); err != nil {
		t.Fatalf("expected source effort restored after rollback: %v", err)
	}
	if _, err := os.Stat(fixture.targetRoot); !os.IsNotExist(err) {
		t.Fatalf("expected target effort removed after rollback, stat err=%v", err)
	}
	after := snapshotMigrationApplyTree(t, fixture.root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("expected rollback to restore original workflow/index/events after sync failure, before=%#v after=%#v", before, after)
	}
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

func newMigrationApplyFixture(t *testing.T) *migrationApplyFixture {
	t.Helper()
	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	sourceRoot := filepath.Join(effortsRoot, "started", "test-effort")
	targetRoot := filepath.Join(effortsRoot, "closed", "test-effort")
	workflowState := migrationTestWorkflowState("started", "reviewing")
	index := migrationTestEffortIndex(workflowState)

	writeMigrationApplyJSON(t, filepath.Join(effortsRoot, "index.json"), index)
	writeMigrationApplyJSON(t, filepath.Join(sourceRoot, "workflow_state.json"), workflowState)
	writeMigrationApplyText(t, filepath.Join(sourceRoot, "events.jsonl"), `{"event":"workflow_state_updated","event_id":"evt-007","effort_id":"test-effort","task_id":"impl-test","timestamp":"2026-05-18T21:00:00Z","source":"fixture","details":{"state_revision":7}}`+"\n")
	writeMigrationApplyText(t, filepath.Join(sourceRoot, "README.md"), "see briefs/test.md\n")
	writeMigrationApplyText(t, filepath.Join(sourceRoot, "briefs", "test.md"), "# Test Brief\n")

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

func snapshotMigrationApplyTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
	}); err != nil {
		t.Fatalf("snapshot tree %s: %v", root, err)
	}
	return snapshot
}

func writeMigrationApplyJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := marshalIndentedJSON(value)
	if err != nil {
		t.Fatalf("marshal JSON for %s: %v", path, err)
	}
	writeMigrationApplyText(t, path, string(data))
}

func writeMigrationApplyText(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
