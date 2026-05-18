package productworkflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ProductStatusMigrationApplyOptions struct {
	EffortRoot                 string
	EffortsRoot                string
	TargetStatusBucket         string
	TargetCurrentState         string
	UpdatedAt                  string
	MigrationEventID           string
	WorkspaceFingerprint       string
	ExpectedStateRevision      int
	ExpectedSourceStatusBucket string
}

type productStatusMigrationDiskState struct {
	absEffortRoot    string
	absEffortsRoot   string
	sourceFolderPath string
	eventsPath       string
	workflowState    WorkflowState
	effortIndex      EffortIndex
	workflowBytes    []byte
	indexBytes       []byte
	eventsBytes      []byte
	plannerRoot      string
}

var productStatusMigrationAfterFolderRenameHook func() error

func PlanProductStatusMigrationFromEffortRoot(options ProductStatusMigrationApplyOptions) ProductStatusMigrationPlan {
	plan := ProductStatusMigrationPlan{
		Status:             "blocked",
		TargetStatusBucket: strings.ToLower(strings.TrimSpace(options.TargetStatusBucket)),
		Blockers:           []ProductStatusMigrationBlocker{},
		NextValidAction:    "resolve status migration blockers before applying a migration",
	}

	state, ok := loadProductStatusMigrationDiskState(options, &plan)
	if !ok {
		return plan
	}

	plan = PlanProductStatusMigration(state.workflowState, state.effortIndex, ProductStatusMigrationOptions{
		SourceFolderPath:     state.sourceFolderPath,
		TargetStatusBucket:   options.TargetStatusBucket,
		TargetCurrentState:   options.TargetCurrentState,
		UpdatedAt:            options.UpdatedAt,
		MigrationEventID:     options.MigrationEventID,
		WorkspaceFingerprint: options.WorkspaceFingerprint,
		EffortsRoot:          state.plannerRoot,
	})
	return plan
}

func ApplyProductStatusMigration(options ProductStatusMigrationApplyOptions) ProductStatusMigrationPlan {
	plan := ProductStatusMigrationPlan{
		Status:             "blocked",
		TargetStatusBucket: strings.ToLower(strings.TrimSpace(options.TargetStatusBucket)),
		Blockers:           []ProductStatusMigrationBlocker{},
		NextValidAction:    "resolve status migration blockers before applying a migration",
	}

	state, ok := loadProductStatusMigrationDiskState(options, &plan)
	if !ok {
		return plan
	}

	plan = PlanProductStatusMigration(state.workflowState, state.effortIndex, ProductStatusMigrationOptions{
		SourceFolderPath:     state.sourceFolderPath,
		TargetStatusBucket:   options.TargetStatusBucket,
		TargetCurrentState:   options.TargetCurrentState,
		UpdatedAt:            options.UpdatedAt,
		MigrationEventID:     options.MigrationEventID,
		WorkspaceFingerprint: options.WorkspaceFingerprint,
		EffortsRoot:          state.plannerRoot,
	})
	if len(plan.Blockers) > 0 {
		return plan
	}

	if options.ExpectedStateRevision < 0 {
		plan.addBlocker(
			"missing_expected_revision",
			"expected_revision",
			"status migration apply requires an expected workflow_state revision",
		)
	}
	plan.Event.Event = "status_migration_applied"
	plan.Event.Source = "product_status_migration_apply"

	if options.ExpectedStateRevision >= 0 && state.workflowState.StateRevision != options.ExpectedStateRevision {
		plan.addBlocker(
			"stale_workflow_state_revision",
			"workflow_state.json",
			fmt.Sprintf("workflow state revision %d does not match expected revision %d", state.workflowState.StateRevision, options.ExpectedStateRevision),
		)
	}
	if expectedBucket := strings.TrimSpace(options.ExpectedSourceStatusBucket); expectedBucket != "" && state.workflowState.StatusBucket != expectedBucket {
		plan.addBlocker(
			"stale_source_status_bucket",
			"workflow_state.json",
			fmt.Sprintf("workflow status_bucket %q does not match expected source status_bucket %q", state.workflowState.StatusBucket, expectedBucket),
		)
	}

	targetRoot := filepath.Join(state.absEffortsRoot, filepath.FromSlash(plan.TargetFolderPath))
	if rel, err := filepath.Rel(state.absEffortsRoot, targetRoot); err != nil || !safeMigrationRelativePath(rel) {
		plan.addBlocker("unsafe_target_folder_path", "target_folder_path", "target folder path must stay within the efforts root")
	}
	if err := rejectProductStatusMigrationSymlinks(state.absEffortsRoot, filepath.Dir(targetRoot)); err != nil {
		plan.addBlocker("unsafe_target_folder_symlink", "target_folder_path", err.Error())
	}
	if info, err := os.Stat(state.absEffortRoot); err != nil {
		if os.IsNotExist(err) {
			plan.addBlocker("missing_source_folder", "source_folder_path", "source effort folder not found")
		} else {
			plan.addBlocker("read_source_folder_error", "source_folder_path", err.Error())
		}
	} else if !info.IsDir() {
		plan.addBlocker("invalid_source_folder", "source_folder_path", "source effort root is not a directory")
	}
	if _, err := os.Stat(targetRoot); err == nil {
		plan.addBlocker("target_folder_exists", "target_folder_path", fmt.Sprintf("target effort folder %q already exists", plan.TargetFolderPath))
	} else if !os.IsNotExist(err) {
		plan.addBlocker("read_target_folder_error", "target_folder_path", err.Error())
	}
	if err := validateProductStatusMigrationEventsJSONL(state.eventsBytes); err != nil {
		plan.addBlocker("malformed_events_jsonl", state.eventsPath, err.Error())
	}
	if len(plan.Blockers) > 0 {
		plan.Status = "blocked"
		return plan
	}

	updatedWorkflowBytes, err := marshalIndentedJSON(plan.UpdatedWorkflowState)
	if err != nil {
		plan.addBlocker("workflow_state_encode_error", "workflow_state.json", err.Error())
		return plan
	}
	updatedIndexBytes, err := marshalIndentedJSON(plan.UpdatedEffortIndex)
	if err != nil {
		plan.addBlocker("effort_index_encode_error", "efforts/index.json", err.Error())
		return plan
	}
	updatedEventsBytes, err := appendProductStatusMigrationEvent(state.eventsBytes, plan.Event)
	if err != nil {
		plan.addBlocker("events_encode_error", state.eventsPath, err.Error())
		return plan
	}

	workflowTemp := filepath.Join(state.absEffortRoot, "workflow_state.json.status-migration.tmp")
	eventsTemp := filepath.Join(state.absEffortRoot, filepath.FromSlash(state.eventsPath)+".status-migration.tmp")
	indexPath := filepath.Join(state.absEffortsRoot, "index.json")

	if err := writeMigrationTempFile(workflowTemp, updatedWorkflowBytes); err != nil {
		plan.addBlocker("workflow_state_stage_error", "workflow_state.json", err.Error())
		return plan
	}
	if err := writeMigrationTempFile(eventsTemp, updatedEventsBytes); err != nil {
		_ = os.Remove(workflowTemp)
		plan.addBlocker("events_stage_error", state.eventsPath, err.Error())
		return plan
	}
	if err := os.MkdirAll(filepath.Dir(targetRoot), 0o755); err != nil {
		_ = os.Remove(workflowTemp)
		_ = os.Remove(eventsTemp)
		plan.addBlocker("target_folder_prepare_error", "target_folder_path", err.Error())
		return plan
	}

	renamed := false
	workflowReplaced := false
	eventsReplaced := false
	indexReplaced := false
	cleanupTemps := func(root string) {
		_ = os.Remove(filepath.Join(root, "workflow_state.json.status-migration.tmp"))
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(state.eventsPath)+".status-migration.tmp"))
		_ = os.Remove(filepath.Join(state.absEffortsRoot, "index.json.status-migration.tmp"))
	}
	rollback := func(applyErr error) ProductStatusMigrationPlan {
		rollbackErr := rollbackProductStatusMigrationFiles(productStatusMigrationRollback{
			renamed:           renamed,
			workflowReplaced:  workflowReplaced,
			eventsReplaced:    eventsReplaced,
			indexReplaced:     indexReplaced,
			sourceRoot:        state.absEffortRoot,
			targetRoot:        targetRoot,
			eventsPath:        state.eventsPath,
			indexPath:         indexPath,
			originalWorkflow:  state.workflowBytes,
			originalEvents:    state.eventsBytes,
			originalIndex:     state.indexBytes,
			sourceCleanupRoot: state.absEffortRoot,
			targetCleanupRoot: targetRoot,
		})
		cleanupTemps(state.absEffortRoot)
		cleanupTemps(targetRoot)
		plan.Status = "blocked"
		if rollbackErr != nil {
			plan.addBlocker("status_migration_rollback_failed", "", fmt.Sprintf("%v (rollback error: %v)", applyErr, rollbackErr))
		} else {
			plan.addBlocker("status_migration_apply_error", "", applyErr.Error())
		}
		plan.NextValidAction = "inspect migration blockers and rerun status migration apply after restoring a clean effort root"
		return plan
	}

	if err := os.Rename(state.absEffortRoot, targetRoot); err != nil {
		cleanupTemps(state.absEffortRoot)
		plan.addBlocker("status_migration_apply_error", "source_folder_path", err.Error())
		plan.NextValidAction = "inspect migration blockers and rerun status migration apply after restoring a clean effort root"
		return plan
	}
	renamed = true
	if err := syncDirBestEffort(filepath.Dir(state.absEffortRoot)); err != nil {
		return rollback(err)
	}
	if targetParent := filepath.Dir(targetRoot); targetParent != filepath.Dir(state.absEffortRoot) {
		if err := syncDirBestEffort(targetParent); err != nil {
			return rollback(err)
		}
	}
	if productStatusMigrationAfterFolderRenameHook != nil {
		if err := productStatusMigrationAfterFolderRenameHook(); err != nil {
			return rollback(err)
		}
	}

	targetWorkflowTemp := filepath.Join(targetRoot, "workflow_state.json.status-migration.tmp")
	targetEventsTemp := filepath.Join(targetRoot, filepath.FromSlash(state.eventsPath)+".status-migration.tmp")
	if err := os.Rename(targetWorkflowTemp, filepath.Join(targetRoot, "workflow_state.json")); err != nil {
		return rollback(err)
	}
	if err := syncFileBestEffort(filepath.Join(targetRoot, "workflow_state.json")); err != nil {
		return rollback(err)
	}
	if err := syncDirBestEffort(targetRoot); err != nil {
		return rollback(err)
	}
	workflowReplaced = true
	if err := os.Rename(targetEventsTemp, filepath.Join(targetRoot, filepath.FromSlash(state.eventsPath))); err != nil {
		return rollback(err)
	}
	if err := syncFileBestEffort(filepath.Join(targetRoot, filepath.FromSlash(state.eventsPath))); err != nil {
		return rollback(err)
	}
	if err := syncDirBestEffort(filepath.Dir(filepath.Join(targetRoot, filepath.FromSlash(state.eventsPath)))); err != nil {
		return rollback(err)
	}
	eventsReplaced = true
	if err := atomicWriteFile(indexPath, updatedIndexBytes, 0o644); err != nil {
		return rollback(err)
	}
	indexReplaced = true

	plan.Status = "applied"
	plan.NextValidAction = fmt.Sprintf("inspect migrated effort under %s and rerun `idiom product status --effort-root <path>`", plan.TargetFolderPath)
	return plan
}

type productStatusMigrationRollback struct {
	renamed           bool
	workflowReplaced  bool
	eventsReplaced    bool
	indexReplaced     bool
	sourceRoot        string
	targetRoot        string
	eventsPath        string
	indexPath         string
	originalWorkflow  []byte
	originalEvents    []byte
	originalIndex     []byte
	sourceCleanupRoot string
	targetCleanupRoot string
}

func rollbackProductStatusMigrationFiles(input productStatusMigrationRollback) error {
	if !input.renamed {
		return nil
	}
	if err := os.Rename(input.targetRoot, input.sourceRoot); err != nil {
		return fmt.Errorf("rename migrated effort back to source: %w", err)
	}
	if err := syncDirBestEffort(filepath.Dir(input.sourceRoot)); err != nil {
		return fmt.Errorf("sync source parent after rollback rename: %w", err)
	}
	if targetParent := filepath.Dir(input.targetRoot); targetParent != filepath.Dir(input.sourceRoot) {
		if err := syncDirBestEffort(targetParent); err != nil {
			return fmt.Errorf("sync target parent after rollback rename: %w", err)
		}
	}
	if input.workflowReplaced {
		if err := atomicWriteFile(filepath.Join(input.sourceRoot, "workflow_state.json"), input.originalWorkflow, 0o644); err != nil {
			return fmt.Errorf("restore workflow_state.json: %w", err)
		}
	}
	if input.eventsReplaced {
		if err := atomicWriteFile(filepath.Join(input.sourceRoot, filepath.FromSlash(input.eventsPath)), input.originalEvents, 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", input.eventsPath, err)
		}
	}
	if input.indexReplaced {
		if err := atomicWriteFile(input.indexPath, input.originalIndex, 0o644); err != nil {
			return fmt.Errorf("restore efforts/index.json: %w", err)
		}
	}
	return nil
}

func loadProductStatusMigrationDiskState(options ProductStatusMigrationApplyOptions, plan *ProductStatusMigrationPlan) (productStatusMigrationDiskState, bool) {
	state := productStatusMigrationDiskState{}
	effortRoot := strings.TrimSpace(options.EffortRoot)
	absEffortRoot, err := filepath.Abs(effortRoot)
	if effortRoot == "" || err != nil {
		plan.addBlocker("missing_effort_root", "effort_root", "effort root is required")
		plan.NextValidAction = "rerun status migration with --effort-root pointing at one effort folder"
		return state, false
	}
	if hasPathTraversal(effortRoot) {
		plan.addBlocker("unsafe_effort_root", "effort_root", "effort root must not contain traversal components")
		plan.NextValidAction = "rerun status migration with a safe effort root"
		return state, false
	}
	state.absEffortRoot = absEffortRoot

	if strings.TrimSpace(options.EffortsRoot) != "" && hasPathTraversal(options.EffortsRoot) {
		plan.addBlocker("unsafe_efforts_root", "efforts_root", "efforts root must not contain traversal components")
	}

	absEffortsRoot := strings.TrimSpace(options.EffortsRoot)
	if absEffortsRoot == "" {
		absEffortsRoot = filepath.Dir(filepath.Dir(absEffortRoot))
	}
	absEffortsRoot, err = filepath.Abs(absEffortsRoot)
	if err != nil {
		plan.addBlocker("unsafe_efforts_root", "efforts_root", fmt.Sprintf("resolve efforts root: %v", err))
		return state, false
	}
	state.absEffortsRoot = absEffortsRoot
	effortsRootOK := true
	if isSymlink, symlinkErr := productStatusMigrationPathIsSymlink(absEffortsRoot); symlinkErr != nil {
		plan.addBlocker("read_efforts_root_error", "efforts_root", symlinkErr.Error())
		effortsRootOK = false
	} else if isSymlink {
		plan.addBlocker("unsafe_efforts_root_symlink", "efforts_root", "efforts root must not be a symlink")
		effortsRootOK = false
	}
	if !effortsRootOK {
		return state, false
	}

	sourceFolderPath, relErr := filepath.Rel(absEffortsRoot, absEffortRoot)
	sourceRootOK := relErr == nil
	if relErr != nil {
		plan.addBlocker("effort_root_outside_efforts_root", "effort_root", "effort root must stay within the efforts root")
	} else {
		sourceFolderPath = filepath.ToSlash(sourceFolderPath)
	}
	if !safeMigrationRelativePath(sourceFolderPath) {
		if sourceFolderPath == "." || sourceFolderPath == "" {
			plan.addBlocker("unsafe_source_folder_path", "effort_root", "effort root must resolve to a status bucket and effort folder inside the efforts root")
		} else {
			plan.addBlocker("effort_root_outside_efforts_root", "effort_root", "effort root must stay within the efforts root")
		}
		sourceRootOK = false
	} else {
		state.sourceFolderPath = sourceFolderPath
		plan.SourceFolderPath = sourceFolderPath
	}
	if sourceRootOK {
		if err := rejectProductStatusMigrationSymlinks(absEffortsRoot, absEffortRoot); err != nil {
			plan.addBlocker("unsafe_effort_root_symlink", "effort_root", err.Error())
			sourceRootOK = false
		}
	}
	if !sourceRootOK {
		return state, false
	}

	if info, statErr := os.Stat(absEffortRoot); statErr != nil {
		if os.IsNotExist(statErr) {
			plan.addBlocker("missing_source_folder", "effort_root", "source effort folder not found")
		} else {
			plan.addBlocker("read_source_folder_error", "effort_root", statErr.Error())
		}
	} else if !info.IsDir() {
		plan.addBlocker("invalid_source_folder", "effort_root", "effort root is not a directory")
	}

	indexPath := filepath.Join(absEffortsRoot, "index.json")
	if err := rejectProductStatusMigrationSymlinks(absEffortsRoot, indexPath); err != nil {
		plan.addBlocker("unsafe_effort_index_symlink", "efforts/index.json", err.Error())
	} else {
		indexBytes, indexErr := os.ReadFile(indexPath)
		if indexErr != nil {
			if os.IsNotExist(indexErr) {
				plan.addBlocker("missing_effort_index", "efforts/index.json", "effort index not found")
			} else {
				plan.addBlocker("read_effort_index_error", "efforts/index.json", indexErr.Error())
			}
		} else {
			state.indexBytes = indexBytes
			idx, parseErr := ParseEffortIndexJSON(indexBytes)
			if parseErr != nil {
				plan.addBlocker("malformed_effort_index", "efforts/index.json", parseErr.Error())
			} else {
				state.effortIndex = idx
			}
		}
	}

	workflowPath := filepath.Join(absEffortRoot, "workflow_state.json")
	if err := rejectProductStatusMigrationSymlinks(absEffortRoot, workflowPath); err != nil {
		plan.addBlocker("unsafe_workflow_state_symlink", "workflow_state.json", err.Error())
		return state, false
	}
	workflowBytes, workflowErr := os.ReadFile(workflowPath)
	if workflowErr != nil {
		if os.IsNotExist(workflowErr) {
			plan.addBlocker("missing_workflow_state", "workflow_state.json", "workflow_state.json not found")
		} else {
			plan.addBlocker("read_workflow_state_error", "workflow_state.json", workflowErr.Error())
		}
		return state, false
	}
	state.workflowBytes = workflowBytes
	workflowState, parseErr := ParseWorkflowStateJSON(workflowBytes)
	if parseErr != nil {
		plan.addBlocker("malformed_workflow_state", "workflow_state.json", parseErr.Error())
		return state, false
	}
	state.workflowState = workflowState
	plan.EffortID = workflowState.EffortID
	plan.SourceStatusBucket = workflowState.StatusBucket

	eventsPath, eventsPathOK := normalizedSafeEffortPath(workflowState.ArtifactPaths["events"])
	if strings.TrimSpace(workflowState.ArtifactPaths["events"]) == "" {
		plan.addBlocker("missing_events_artifact_path", "workflow_state.json", "workflow_state artifact_paths.events is required for status migration")
	} else if !eventsPathOK {
		plan.addBlocker("unsafe_events_artifact_path", "workflow_state.json", "workflow_state artifact_paths.events must be a safe relative path")
	} else {
		state.eventsPath = eventsPath
		eventsFullPath := filepath.Join(absEffortRoot, filepath.FromSlash(eventsPath))
		if err := rejectProductStatusMigrationSymlinks(absEffortRoot, eventsFullPath); err != nil {
			plan.addBlocker("unsafe_events_file_symlink", eventsPath, err.Error())
		} else {
			eventsBytes, eventsErr := os.ReadFile(eventsFullPath)
			if eventsErr != nil {
				if os.IsNotExist(eventsErr) {
					plan.addBlocker("missing_events_file", eventsPath, "events ledger not found")
				} else {
					plan.addBlocker("read_events_file_error", eventsPath, eventsErr.Error())
				}
			} else {
				state.eventsBytes = eventsBytes
			}
		}
	}

	if sourceRootOK {
		state.plannerRoot = deriveProductStatusMigrationPlannerRoot(workflowState.FolderPath, state.sourceFolderPath)
	}
	if len(plan.Blockers) > 0 {
		return state, false
	}
	return state, true
}

func deriveProductStatusMigrationPlannerRoot(folderPath, sourceFolderPath string) string {
	normalizedFolder, ok := normalizedSafeEffortPath(folderPath)
	if !ok || !folderPathMatchesRelative(normalizedFolder, sourceFolderPath) {
		return DefaultProductEffortsRoot
	}
	suffix := "/" + normalizeEffortRelPath(sourceFolderPath)
	if !strings.HasSuffix(normalizedFolder, suffix) {
		return DefaultProductEffortsRoot
	}
	root := strings.TrimSuffix(normalizedFolder, suffix)
	if normalized, ok := normalizedSafeEffortPath(root); ok {
		return normalized
	}
	return DefaultProductEffortsRoot
}

func appendProductStatusMigrationEvent(existing []byte, event ProductStatusMigrationEvent) ([]byte, error) {
	eventBytes, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal status migration event: %w", err)
	}
	trimmed := bytes.TrimRight(existing, "\n")
	out := append([]byte{}, trimmed...)
	if len(out) > 0 {
		out = append(out, '\n')
	}
	out = append(out, eventBytes...)
	out = append(out, '\n')
	return out, nil
}

func validateProductStatusMigrationEventsJSONL(data []byte) error {
	scanner := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, line := range scanner {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			return fmt.Errorf("parse events line %d: %w", i+1, err)
		}
	}
	return nil
}

func marshalIndentedJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeMigrationTempFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	if err := syncFileBestEffort(path); err != nil {
		return err
	}
	return syncDirBestEffort(filepath.Dir(path))
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tempPath := path + ".status-migration.tmp"
	if err := writeMigrationTempFile(tempPath, data); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := syncFileBestEffort(path); err != nil {
		return err
	}
	if err := syncDirBestEffort(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

func rejectProductStatusMigrationSymlinks(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("resolve relative path: %w", err)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("path %q must stay within %q", target, root)
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			offendingRel, _ := filepath.Rel(root, current)
			return fmt.Errorf("path component %q must not be a symlink", filepath.ToSlash(offendingRel))
		}
	}
	return nil
}

func productStatusMigrationPathIsSymlink(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

func syncFileBestEffort(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirBestEffort(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func safeMigrationRelativePath(value string) bool {
	normalized, ok := normalizedSafeEffortPath(value)
	if !ok {
		return false
	}
	return strings.Contains(normalized, "/")
}
