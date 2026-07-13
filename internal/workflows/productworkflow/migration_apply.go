package productworkflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"idiom/internal/platform/atomicfile"
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

const (
	productStatusMigrationIndexLockRelPath      = ".idiom-efforts-index.lock"
	productStatusMigrationTransactionRelPath    = ".idiom-product-status-migration-transaction"
	productStatusMigrationManifestFileName      = "manifest.json"
	productStatusMigrationCandidateWorkflowName = "candidate-workflow-state.json"
	productStatusMigrationCandidateEventsName   = "candidate-events.jsonl"
	productStatusMigrationCandidateIndexName    = "candidate-index.json"
	productStatusMigrationPendingMarkerName     = ".idiom-product-status-migration-pending.json"
	productStatusMigrationManifestSchema        = "idiom.product_status_migration.v1"
	productStatusMigrationTransactionPrepared   = "prepared"
	productStatusMigrationTransactionCommitted  = "committed"
)

type productStatusMigrationManifest struct {
	SchemaVersion           string `json:"schema_version"`
	State                   string `json:"state"`
	SourceFolderPath        string `json:"source_folder_path"`
	TargetFolderPath        string `json:"target_folder_path"`
	EventsPath              string `json:"events_path"`
	EffortID                string `json:"effort_id"`
	EventID                 string `json:"event_id"`
	PreviousWorkflowDigest  string `json:"previous_workflow_digest"`
	CandidateWorkflowDigest string `json:"candidate_workflow_digest"`
	PreviousEventsDigest    string `json:"previous_events_digest"`
	CandidateEventsDigest   string `json:"candidate_events_digest"`
	PreviousIndexDigest     string `json:"previous_index_digest"`
	CandidateIndexDigest    string `json:"candidate_index_digest"`
}

type productStatusMigrationCandidates struct {
	workflow []byte
	events   []byte
	index    []byte
}

type productStatusMigrationRecovery struct {
	manifest   productStatusMigrationManifest
	candidates productStatusMigrationCandidates
	recovered  bool
}

var productStatusMigrationAfterFolderRenameHook func() error
var productStatusMigrationSyncFile = syncProductStatusMigrationFile
var productStatusMigrationSyncDir = syncProductStatusMigrationDir

func PlanProductStatusMigrationFromEffortRoot(options ProductStatusMigrationApplyOptions) ProductStatusMigrationPlan {
	plan := ProductStatusMigrationPlan{
		Status:             ProductWorkflowStatusBlocked,
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
		Status:             ProductWorkflowStatusBlocked,
		TargetStatusBucket: strings.ToLower(strings.TrimSpace(options.TargetStatusBucket)),
		Blockers:           []ProductStatusMigrationBlocker{},
		NextValidAction:    "resolve status migration blockers before applying a migration",
	}
	state, ok := resolveProductStatusMigrationApplyRoots(options, &plan)
	if !ok {
		return plan
	}

	// The efforts-root lock is the outer authority for both recovery and new
	// migrations. Recovery runs before opening the source effort because an
	// interrupted rename can make the caller's original path disappear.
	indexLock, err := acquireEffortFileLock(
		state.absEffortsRoot,
		productStatusMigrationIndexLockRelPath,
		"product status migration index",
	)
	if err != nil {
		plan.addBlocker("effort_index_lock_error", "efforts/index.json", err.Error())

		return plan
	}
	defer indexLock.Close()

	recovery, err := recoverProductStatusMigrationLocked(indexLock)
	if err != nil {
		plan.addBlocker("status_migration_recovery_error", productStatusMigrationTransactionRelPath, err.Error())
		plan.NextValidAction = "repair or complete the product status migration journal before retrying"

		return plan
	}
	if recovery.recovered && recovery.manifest.SourceFolderPath == state.sourceFolderPath {
		return recoveredProductStatusMigrationPlan(plan, recovery)
	}

	workflowLease, err := openProductWorkflowMutationLease(state.absEffortRoot)
	if err != nil {
		plan.addBlocker("product_workflow_lock_error", WorkflowStateFileName, err.Error())

		return plan
	}
	defer workflowLease.Close()

	lockedPlan := ProductStatusMigrationPlan{
		Status:             ProductWorkflowStatusBlocked,
		TargetStatusBucket: strings.ToLower(strings.TrimSpace(options.TargetStatusBucket)),
		SourceFolderPath:   state.sourceFolderPath,
		Blockers:           []ProductStatusMigrationBlocker{},
		NextValidAction:    "resolve status migration blockers before applying a migration",
	}
	lockedState, ok := loadProductStatusMigrationDiskStateLocked(
		state,
		workflowLease.root,
		indexLock.root,
		&lockedPlan,
	)
	if !ok {
		return lockedPlan
	}
	state = lockedState
	plan = lockedPlan
	if err := indexLock.ValidatePathIdentity(); err != nil {
		plan.addBlocker("stale_efforts_root_identity", "efforts/index.json", err.Error())

		return plan
	}
	if err := workflowLease.ValidatePathIdentity(); err != nil {
		plan.addBlocker("stale_effort_root_identity", WorkflowStateFileName, err.Error())

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
		plan.addBlocker("missing_expected_revision", "expected_revision", "status migration apply requires an expected workflow_state revision")
	}
	plan.Event.Event = "status_migration_applied"
	plan.Event.Source = "product_status_migration_apply"
	if options.ExpectedStateRevision >= 0 && state.workflowState.StateRevision != options.ExpectedStateRevision {
		plan.addBlocker(
			"stale_workflow_state_revision",
			WorkflowStateFileName,
			fmt.Sprintf("workflow state revision %d does not match expected revision %d", state.workflowState.StateRevision, options.ExpectedStateRevision),
		)
	}
	if expectedBucket := strings.TrimSpace(options.ExpectedSourceStatusBucket); expectedBucket != "" && state.workflowState.StatusBucket != expectedBucket {
		plan.addBlocker(
			"stale_source_status_bucket",
			WorkflowStateFileName,
			fmt.Sprintf("workflow status_bucket %q does not match expected source status_bucket %q", state.workflowState.StatusBucket, expectedBucket),
		)
	}
	if err := validateProductStatusMigrationEventsJSONL(state.eventsBytes); err != nil {
		plan.addBlocker("malformed_events_jsonl", state.eventsPath, err.Error())
	}

	targetRel := filepath.FromSlash(plan.TargetFolderPath)
	targetRoot := filepath.Join(state.absEffortsRoot, targetRel)
	if rel, err := filepath.Rel(state.absEffortsRoot, targetRoot); err != nil || !safeMigrationRelativePath(filepath.ToSlash(rel)) {
		plan.addBlocker("unsafe_target_folder_path", "target_folder_path", "target folder path must stay within the efforts root")
	}
	if err := ensureProductStatusMigrationRootPathAbsent(indexLock.root, targetRel); err != nil {
		plan.addBlocker("target_folder_exists", "target_folder_path", err.Error())
	}
	if len(plan.Blockers) > 0 {
		plan.Status = ProductWorkflowStatusBlocked

		return plan
	}

	updatedWorkflowBytes, err := marshalIndentedJSON(plan.UpdatedWorkflowState)
	if err != nil {
		plan.addBlocker("workflow_state_encode_error", WorkflowStateFileName, err.Error())

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

	candidates := productStatusMigrationCandidates{
		workflow: updatedWorkflowBytes,
		events:   updatedEventsBytes,
		index:    updatedIndexBytes,
	}
	manifest := productStatusMigrationManifest{
		SchemaVersion:           productStatusMigrationManifestSchema,
		State:                   productStatusMigrationTransactionPrepared,
		SourceFolderPath:        state.sourceFolderPath,
		TargetFolderPath:        plan.TargetFolderPath,
		EventsPath:              state.eventsPath,
		EffortID:                state.workflowState.EffortID,
		EventID:                 plan.Event.EventID,
		PreviousWorkflowDigest:  productStatusMigrationDigest(state.workflowBytes),
		CandidateWorkflowDigest: productStatusMigrationDigest(updatedWorkflowBytes),
		PreviousEventsDigest:    productStatusMigrationDigest(state.eventsBytes),
		CandidateEventsDigest:   productStatusMigrationDigest(updatedEventsBytes),
		PreviousIndexDigest:     productStatusMigrationDigest(state.indexBytes),
		CandidateIndexDigest:    productStatusMigrationDigest(updatedIndexBytes),
	}
	if err := prepareProductStatusMigration(indexLock.root, manifest, candidates); err != nil {
		plan.addBlocker("status_migration_prepare_error", productStatusMigrationTransactionRelPath, err.Error())

		return plan
	}
	abortPrepared := func(code, path string, cause error) ProductStatusMigrationPlan {
		plan.Status = ProductWorkflowStatusBlocked
		plan.addBlocker(code, path, abortProductStatusMigration(indexLock.root, cause).Error())

		return plan
	}

	if err := indexLock.ValidatePathIdentity(); err != nil {
		return abortPrepared("stale_efforts_root_identity", "efforts/index.json", err)
	}
	if err := workflowLease.ValidatePathIdentity(); err != nil {
		return abortPrepared("stale_effort_root_identity", WorkflowStateFileName, err)
	}
	if err := ensureEffortArtifactParents(indexLock.root, targetRel); err != nil {
		return abortPrepared("target_folder_prepare_error", "target_folder_path", err)
	}
	if err := ensureProductStatusMigrationRootPathAbsent(indexLock.root, targetRel); err != nil {
		return abortPrepared("target_folder_exists", "target_folder_path", err)
	}
	if err := writeProductStatusMigrationPendingMarker(workflowLease.root, manifest); err != nil {
		plan.Status = ProductWorkflowStatusBlocked
		plan.addBlocker(
			"status_migration_fence_error",
			productStatusMigrationPendingMarkerName,
			abortProductStatusMigrationBeforeRename(indexLock.root, workflowLease.root, err).Error(),
		)

		return plan
	}
	abortFenced := func(code, path string, cause error) ProductStatusMigrationPlan {
		plan.Status = ProductWorkflowStatusBlocked
		plan.addBlocker(code, path, abortProductStatusMigrationBeforeRename(indexLock.root, workflowLease.root, cause).Error())

		return plan
	}
	if _, err := validateProductStatusMigrationPendingMarker(workflowLease.root, manifest); err != nil {
		return abortFenced("status_migration_fence_error", productStatusMigrationPendingMarkerName, err)
	}
	if err := indexLock.ValidatePathIdentity(); err != nil {
		return abortFenced("stale_efforts_root_identity", "efforts/index.json", err)
	}
	if err := workflowLease.ValidatePathIdentity(); err != nil {
		return abortFenced("stale_effort_root_identity", WorkflowStateFileName, err)
	}

	sourceRel := filepath.FromSlash(state.sourceFolderPath)
	if err := indexLock.root.Rename(sourceRel, targetRel); err != nil {
		sourceExists, sourceErr := productStatusMigrationDirectoryExists(indexLock.root, sourceRel)
		targetExists, targetErr := productStatusMigrationDirectoryExists(indexLock.root, targetRel)
		if sourceErr == nil && targetErr == nil && sourceExists && !targetExists {
			return abortFenced("status_migration_apply_error", "source_folder_path", err)
		}
		if targetErr == nil && targetExists {
			return committedProductStatusMigrationPlan(
				plan,
				fmt.Errorf("folder rename reported an error after the target became visible (source_error=%v source_exists=%t): %w", sourceErr, sourceExists, err),
			)
		}

		plan.Status = ProductWorkflowStatusBlocked
		plan.addBlocker(
			"status_migration_rename_ambiguous",
			"source_folder_path",
			fmt.Sprintf("folder rename outcome could not be classified; journal and mutation fence retained (source_error=%v target_error=%v source_exists=%t target_exists=%t): %v", sourceErr, targetErr, sourceExists, targetExists, err),
		)
		plan.NextValidAction = "inspect the retained product status migration journal before retrying"

		return plan
	}

	if err := validateEffortRootPathIdentity(workflowLease.root, targetRoot, "migrated product workflow root"); err != nil {
		return committedProductStatusMigrationPlan(plan, err)
	}
	if err := syncProductStatusMigrationRename(indexLock.root, sourceRel, targetRel, state.absEffortsRoot); err != nil {
		return committedProductStatusMigrationPlan(plan, err)
	}
	if productStatusMigrationAfterFolderRenameHook != nil {
		if err := productStatusMigrationAfterFolderRenameHook(); err != nil {
			return committedProductStatusMigrationPlan(plan, err)
		}
	}
	if err := completeProductStatusMigrationLocked(indexLock, workflowLease.root, targetRoot, manifest, candidates); err != nil {
		return committedProductStatusMigrationPlan(plan, err)
	}
	if err := indexLock.ValidatePathIdentity(); err != nil {
		return committedProductStatusMigrationPlan(plan, err)
	}
	if err := validateEffortRootPathIdentity(workflowLease.root, targetRoot, "migrated product workflow root"); err != nil {
		return committedProductStatusMigrationPlan(plan, err)
	}

	plan.Status = ProductWorkflowStatusApplied
	plan.NextValidAction = fmt.Sprintf("inspect migrated effort under %s and rerun `idiom product status --effort-root <path>`", plan.TargetFolderPath)

	return plan
}
func resolveProductStatusMigrationApplyRoots(
	options ProductStatusMigrationApplyOptions,
	plan *ProductStatusMigrationPlan,
) (productStatusMigrationDiskState, bool) {
	state := productStatusMigrationDiskState{}
	effortRoot := strings.TrimSpace(options.EffortRoot)
	if effortRoot == "" {
		plan.addBlocker("missing_effort_root", "effort_root", "effort root is required")

		return state, false
	}
	if hasPathTraversal(effortRoot) {
		plan.addBlocker("unsafe_effort_root", "effort_root", "effort root must not contain traversal components")

		return state, false
	}
	absEffortRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		plan.addBlocker("unsafe_effort_root", "effort_root", fmt.Sprintf("resolve effort root: %v", err))

		return state, false
	}
	state.absEffortRoot = filepath.Clean(absEffortRoot)

	if strings.TrimSpace(options.EffortsRoot) != "" && hasPathTraversal(options.EffortsRoot) {
		plan.addBlocker("unsafe_efforts_root", "efforts_root", "efforts root must not contain traversal components")

		return state, false
	}
	absEffortsRoot := strings.TrimSpace(options.EffortsRoot)
	if absEffortsRoot == "" {
		absEffortsRoot = filepath.Dir(filepath.Dir(state.absEffortRoot))
	}
	absEffortsRoot, err = filepath.Abs(absEffortsRoot)
	if err != nil {
		plan.addBlocker("unsafe_efforts_root", "efforts_root", fmt.Sprintf("resolve efforts root: %v", err))

		return state, false
	}
	state.absEffortsRoot = filepath.Clean(absEffortsRoot)

	sourceFolderPath, err := filepath.Rel(state.absEffortsRoot, state.absEffortRoot)
	if err != nil {
		plan.addBlocker("effort_root_outside_efforts_root", "effort_root", "effort root must stay within the efforts root")

		return state, false
	}
	sourceFolderPath = filepath.ToSlash(sourceFolderPath)
	if !safeMigrationRelativePath(sourceFolderPath) {
		plan.addBlocker("unsafe_source_folder_path", "effort_root", "effort root must resolve to a status bucket and effort folder inside the efforts root")

		return state, false
	}
	state.sourceFolderPath = sourceFolderPath
	plan.SourceFolderPath = sourceFolderPath

	return state, true
}

func openProductWorkflowMutationLeaseForStatusMigration(
	indexLock *effortFileLock,
	effortRoot string,
) (*productWorkflowMutationLease, error) {
	if indexLock == nil || indexLock.root == nil {
		return nil, fmt.Errorf("product status migration index lock is not open")
	}
	if err := indexLock.ValidatePathIdentity(); err != nil {
		return nil, err
	}

	return openProductWorkflowMutationLeaseWithPendingMigration(effortRoot, true)
}

func recoverOrRejectProductStatusMigrationMarkerLocked(root *os.Root, effortRoot string) error {
	marker, err := readProductStatusMigrationPendingMarker(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect pending product status migration: %w", err)
	}
	if err := validateEffortRootPathIdentity(root, effortRoot, "product workflow root with pending migration marker"); err != nil {
		return err
	}

	return fmt.Errorf(
		"product status migration %s -> %s is pending; rerun status migration recovery before mutating this effort",
		marker.SourceFolderPath,
		marker.TargetFolderPath,
	)
}

func writeProductStatusMigrationPendingMarker(root *os.Root, marker productStatusMigrationManifest) error {
	if err := validateProductStatusMigrationManifest(marker); err != nil {
		return err
	}
	body, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	writeErr := atomicfile.WriteFileRoot(root, productStatusMigrationPendingMarkerName, append(body, '\n'), 0o600)
	if writeErr == nil {
		return nil
	}
	if !artifactWriteCommitted(writeErr) {
		return writeErr
	}
	written, err := readProductStatusMigrationPendingMarker(root)
	if err != nil {
		return fmt.Errorf("%v; verify pending migration marker: %w", writeErr, err)
	}
	if !reflect.DeepEqual(written, marker) {
		return fmt.Errorf("%v; pending migration marker differs from requested authority", writeErr)
	}
	if err := atomicfile.SyncDirRoot(root, "."); err != nil {
		return fmt.Errorf("%v; retry pending migration marker directory sync: %w", writeErr, err)
	}

	return nil
}

func readProductStatusMigrationPendingMarker(root *os.Root) (productStatusMigrationManifest, error) {
	body, err := readProductStatusMigrationRootFile(root, productStatusMigrationPendingMarkerName)
	if err != nil {
		return productStatusMigrationManifest{}, err
	}
	var marker productStatusMigrationManifest
	if err := json.Unmarshal(body, &marker); err != nil {
		return productStatusMigrationManifest{}, err
	}
	if err := validateProductStatusMigrationManifest(marker); err != nil {
		return productStatusMigrationManifest{}, err
	}

	return marker, nil
}

func validateProductStatusMigrationPendingMarker(root *os.Root, manifest productStatusMigrationManifest) (productStatusMigrationManifest, error) {
	marker, err := readProductStatusMigrationPendingMarker(root)
	if err != nil {
		return productStatusMigrationManifest{}, err
	}
	markerAuthority := marker
	manifestAuthority := manifest
	markerAuthority.State = ""
	manifestAuthority.State = ""
	if !reflect.DeepEqual(markerAuthority, manifestAuthority) {
		return productStatusMigrationManifest{}, fmt.Errorf("pending migration marker does not match efforts-root journal authority")
	}

	return marker, nil
}

func removeProductStatusMigrationPendingMarker(root *os.Root) error {
	info, err := root.Lstat(productStatusMigrationPendingMarkerName)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("pending product status migration marker must be a regular file and must not be a symlink")
	}
	if err := root.Remove(productStatusMigrationPendingMarkerName); err != nil {
		return err
	}

	return atomicfile.SyncDirRoot(root, ".")
}

func productStatusMigrationPendingMarkerExists(root *os.Root) (bool, error) {
	info, err := root.Lstat(productStatusMigrationPendingMarkerName)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("pending product status migration marker must be a regular file and must not be a symlink")
	}

	return true, nil
}

func productStatusMigrationPendingMarkerExistsAt(root *os.Root, folderPath string) (bool, error) {
	markerPath := filepath.Join(folderPath, productStatusMigrationPendingMarkerName)
	if err := validateEffortArtifactParents(root, markerPath); err != nil {
		return false, err
	}
	info, err := root.Lstat(markerPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("pending product status migration marker must be a regular file and must not be a symlink")
	}

	return true, nil
}

func recoverProductStatusMigrationLocked(indexLock *effortFileLock) (productStatusMigrationRecovery, error) {
	if indexLock == nil || indexLock.root == nil {
		return productStatusMigrationRecovery{}, fmt.Errorf("product status migration index lock is not open")
	}
	if err := indexLock.ValidatePathIdentity(); err != nil {
		return productStatusMigrationRecovery{}, err
	}
	info, err := indexLock.root.Lstat(productStatusMigrationTransactionRelPath)
	if os.IsNotExist(err) {
		return productStatusMigrationRecovery{}, nil
	}
	if err != nil {
		return productStatusMigrationRecovery{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return productStatusMigrationRecovery{}, fmt.Errorf("product status migration transaction path must be a directory and must not be a symlink")
	}

	manifest, err := readProductStatusMigrationManifest(indexLock.root)
	if os.IsNotExist(err) {
		return productStatusMigrationRecovery{}, removeProductStatusMigrationTransaction(indexLock.root)
	}
	if err != nil {
		return productStatusMigrationRecovery{}, fmt.Errorf("read product status migration journal: %w", err)
	}
	candidates, err := readProductStatusMigrationCandidates(indexLock.root, manifest)
	if err != nil {
		return productStatusMigrationRecovery{}, err
	}
	sourceRel := filepath.FromSlash(manifest.SourceFolderPath)
	targetRel := filepath.FromSlash(manifest.TargetFolderPath)
	sourceExists, err := productStatusMigrationDirectoryExists(indexLock.root, sourceRel)
	if err != nil {
		return productStatusMigrationRecovery{}, err
	}
	targetExists, err := productStatusMigrationDirectoryExists(indexLock.root, targetRel)
	if err != nil {
		return productStatusMigrationRecovery{}, err
	}
	if sourceExists && !targetExists {
		if manifest.State == productStatusMigrationTransactionCommitted {
			return productStatusMigrationRecovery{}, fmt.Errorf("committed product status migration reverted to its source folder")
		}
		sourceRoot := filepath.Join(indexLock.effortRoot, sourceRel)
		workflowLease, err := openProductWorkflowMutationLeaseForStatusMigration(indexLock, sourceRoot)
		if err != nil {
			return productStatusMigrationRecovery{}, fmt.Errorf("open source product workflow for migration abort recovery: %w", err)
		}
		defer workflowLease.Close()
		markerExists, err := productStatusMigrationPendingMarkerExists(workflowLease.root)
		if err != nil {
			return productStatusMigrationRecovery{}, err
		}
		if markerExists {
			if _, err := validateProductStatusMigrationPendingMarker(workflowLease.root, manifest); err != nil {
				return productStatusMigrationRecovery{}, err
			}
			if err := removeProductStatusMigrationPendingMarker(workflowLease.root); err != nil {
				return productStatusMigrationRecovery{}, err
			}
		}

		return productStatusMigrationRecovery{}, removeProductStatusMigrationTransaction(indexLock.root)
	}
	if !sourceExists && !targetExists {
		return productStatusMigrationRecovery{}, fmt.Errorf("product status migration journal has ambiguous source/target folder state")
	}
	if manifest.State == productStatusMigrationTransactionCommitted && !sourceExists && targetExists {
		markerExists, err := productStatusMigrationPendingMarkerExistsAt(indexLock.root, targetRel)
		if err != nil {
			return productStatusMigrationRecovery{}, err
		}
		if !markerExists {
			if err := syncProductStatusMigrationRename(indexLock.root, sourceRel, targetRel, indexLock.effortRoot); err != nil {
				return productStatusMigrationRecovery{}, err
			}
			if err := removeProductStatusMigrationTransaction(indexLock.root); err != nil {
				return productStatusMigrationRecovery{}, err
			}

			return productStatusMigrationRecovery{manifest: manifest, candidates: candidates, recovered: true}, nil
		}
	}

	targetRoot := filepath.Join(indexLock.effortRoot, targetRel)
	workflowLease, err := openProductWorkflowMutationLeaseForStatusMigration(indexLock, targetRoot)
	if err != nil {
		return productStatusMigrationRecovery{}, fmt.Errorf("open migrated product workflow for recovery: %w", err)
	}
	defer workflowLease.Close()
	if sourceExists && targetExists {
		sourceInfo, err := indexLock.root.Lstat(sourceRel)
		if err != nil {
			return productStatusMigrationRecovery{}, err
		}
		targetInfo, err := indexLock.root.Lstat(targetRel)
		if err != nil {
			return productStatusMigrationRecovery{}, err
		}
		sourceGeneration, sourceDigest, err := inspectProductStatusMigrationFolderGeneration(indexLock.root, sourceRel, manifest)
		if err != nil {
			return productStatusMigrationRecovery{}, err
		}
		targetGeneration, targetDigest, err := inspectProductStatusMigrationFolderGeneration(indexLock.root, targetRel, manifest)
		if err != nil {
			return productStatusMigrationRecovery{}, err
		}
		if !os.SameFile(sourceInfo, targetInfo) || sourceGeneration == "unknown" || targetGeneration == "unknown" || sourceDigest != targetDigest {
			return productStatusMigrationRecovery{}, fmt.Errorf(
				"migration source/target replay lacks matching identity and generation evidence (same_file=%t source=%s/%s target=%s/%s)",
				os.SameFile(sourceInfo, targetInfo),
				sourceGeneration,
				sourceDigest,
				targetGeneration,
				targetDigest,
			)
		}
		if _, err := validateProductStatusMigrationPendingMarker(workflowLease.root, manifest); err != nil {
			return productStatusMigrationRecovery{}, err
		}
		// Never recurse through a same-directory alias: a recursive removal would
		// delete the target tree too. Remove only the stale directory entry and
		// leave the journal intact if the filesystem cannot unlink it safely.
		if err := indexLock.root.Remove(sourceRel); err != nil {
			return productStatusMigrationRecovery{}, fmt.Errorf("remove replayed migration source alias without recursion: %w", err)
		}
	}
	if err := syncProductStatusMigrationRename(indexLock.root, sourceRel, targetRel, indexLock.effortRoot); err != nil {
		return productStatusMigrationRecovery{}, err
	}
	if err := completeProductStatusMigrationLocked(indexLock, workflowLease.root, targetRoot, manifest, candidates); err != nil {
		return productStatusMigrationRecovery{}, err
	}

	return productStatusMigrationRecovery{manifest: manifest, candidates: candidates, recovered: true}, nil
}

func prepareProductStatusMigration(
	indexRoot *os.Root,
	manifest productStatusMigrationManifest,
	candidates productStatusMigrationCandidates,
) error {
	if err := validateProductStatusMigrationManifest(manifest); err != nil {
		return err
	}
	if err := validateProductStatusMigrationCandidates(manifest, candidates); err != nil {
		return err
	}
	if err := indexRoot.Mkdir(productStatusMigrationTransactionRelPath, 0o700); err != nil {
		return fmt.Errorf("create product status migration transaction: %w", err)
	}
	if err := atomicfile.SyncDirRoot(indexRoot, "."); err != nil {
		return abortProductStatusMigration(indexRoot, fmt.Errorf("sync product status migration transaction entry: %w", err))
	}
	for _, candidate := range []struct {
		name string
		body []byte
	}{
		{name: productStatusMigrationCandidateWorkflowName, body: candidates.workflow},
		{name: productStatusMigrationCandidateEventsName, body: candidates.events},
		{name: productStatusMigrationCandidateIndexName, body: candidates.index},
	} {
		path := filepath.Join(productStatusMigrationTransactionRelPath, candidate.name)
		if err := writeProductStatusMigrationCandidate(indexRoot, path, candidate.body); err != nil {
			return abortProductStatusMigration(indexRoot, fmt.Errorf("stage %s: %w", candidate.name, err))
		}
	}
	if err := writeProductStatusMigrationManifestDurable(indexRoot, manifest); err != nil {
		return abortProductStatusMigration(indexRoot, fmt.Errorf("prepare product status migration journal: %w", err))
	}

	return nil
}

func completeProductStatusMigrationLocked(
	indexLock *effortFileLock,
	workflowRoot *os.Root,
	targetRoot string,
	manifest productStatusMigrationManifest,
	candidates productStatusMigrationCandidates,
) error {
	if err := indexLock.ValidatePathIdentity(); err != nil {
		return err
	}
	if err := validateEffortRootPathIdentity(workflowRoot, targetRoot, "migrated product workflow root"); err != nil {
		return err
	}
	if err := validateProductStatusMigrationCandidates(manifest, candidates); err != nil {
		return err
	}
	_, err := validateProductStatusMigrationPendingMarker(workflowRoot, manifest)
	if err != nil {
		return fmt.Errorf("validate pending migration fence: %w", err)
	}

	currentWorkflow, err := readProductStatusMigrationRootFile(workflowRoot, WorkflowStateFileName)
	if err != nil {
		return err
	}
	if err := ensureProductStatusMigrationGeneration(
		workflowRoot,
		WorkflowStateFileName,
		currentWorkflow,
		manifest.PreviousWorkflowDigest,
		manifest.CandidateWorkflowDigest,
		candidates.workflow,
	); err != nil {
		return fmt.Errorf("complete migrated workflow state: %w", err)
	}
	if err := verifyProductStatusMigrationWorkflow(candidates.workflow, manifest); err != nil {
		return err
	}
	if err := productStatusMigrationSyncFile(filepath.Join(targetRoot, WorkflowStateFileName)); err != nil {
		return err
	}

	eventsPath := filepath.FromSlash(manifest.EventsPath)
	currentEvents, err := readProductStatusMigrationRootFile(workflowRoot, eventsPath)
	if err != nil {
		return err
	}
	expectedEvent, err := productStatusMigrationEventByID(candidates.events, manifest.EventID)
	if err != nil {
		return err
	}
	currentEvent, currentEventErr := productStatusMigrationEventByID(currentEvents, manifest.EventID)
	if currentEventErr == nil {
		if !reflect.DeepEqual(currentEvent, expectedEvent) {
			return fmt.Errorf("migration event_id %q belongs to a different event", manifest.EventID)
		}
	} else {
		if productStatusMigrationDigest(currentEvents) != manifest.PreviousEventsDigest {
			return fmt.Errorf("events ledger matches neither migration generation and does not contain event_id %q", manifest.EventID)
		}
		if err := writeProductStatusMigrationRootFile(workflowRoot, eventsPath, candidates.events, manifest.CandidateEventsDigest); err != nil {
			return fmt.Errorf("complete migrated events ledger: %w", err)
		}
	}
	if err := productStatusMigrationSyncFile(filepath.Join(targetRoot, eventsPath)); err != nil {
		return err
	}

	currentIndex, err := readProductStatusMigrationRootFile(indexLock.root, "index.json")
	if err != nil {
		return err
	}
	if err := ensureProductStatusMigrationGeneration(
		indexLock.root,
		"index.json",
		currentIndex,
		manifest.PreviousIndexDigest,
		manifest.CandidateIndexDigest,
		candidates.index,
	); err != nil {
		return fmt.Errorf("complete migrated effort index: %w", err)
	}
	if err := indexLock.ValidatePathIdentity(); err != nil {
		return err
	}
	if err := validateEffortRootPathIdentity(workflowRoot, targetRoot, "migrated product workflow root"); err != nil {
		return err
	}

	manifest.State = productStatusMigrationTransactionCommitted
	if err := writeProductStatusMigrationManifestDurable(indexLock.root, manifest); err != nil {
		return fmt.Errorf("finalize product status migration journal: %w", err)
	}
	// The committed outer journal proves workflow/events/index durability. Drop
	// the inner fence while still holding the workflow lease, then remove the
	// outer journal. If the latter cleanup is interrupted, recovery recognizes
	// committed+journal-present+marker-absent as cleanup-only and never rewrites
	// a later ordinary workflow generation.
	if err := removeProductStatusMigrationPendingMarker(workflowRoot); err != nil {
		return err
	}

	return removeProductStatusMigrationTransaction(indexLock.root)
}

func ensureProductStatusMigrationGeneration(
	root *os.Root,
	path string,
	current []byte,
	previousDigest string,
	candidateDigest string,
	candidate []byte,
) error {
	currentDigest := productStatusMigrationDigest(current)
	if currentDigest == candidateDigest {
		return nil
	}
	if currentDigest != previousDigest {
		return fmt.Errorf("%s matches neither migration generation", filepath.ToSlash(path))
	}

	return writeProductStatusMigrationRootFile(root, path, candidate, candidateDigest)
}

func writeProductStatusMigrationRootFile(root *os.Root, path string, body []byte, expectedDigest string) error {
	writeErr := atomicfile.WriteFileRoot(root, path, body, 0o644)
	if writeErr != nil && !artifactWriteCommitted(writeErr) {
		return writeErr
	}
	written, err := readProductStatusMigrationRootFile(root, path)
	if err != nil {
		return fmt.Errorf("%v; verify migration write: %w", writeErr, err)
	}
	if digest := productStatusMigrationDigest(written); digest != expectedDigest {
		return fmt.Errorf("%v; migration write digest %s does not match candidate %s", writeErr, digest, expectedDigest)
	}
	if writeErr != nil {
		if err := atomicfile.SyncDirRoot(root, filepath.Dir(path)); err != nil {
			return fmt.Errorf("%v; retry migration parent sync: %w", writeErr, err)
		}
	}

	return nil
}

func writeProductStatusMigrationCandidate(root *os.Root, path string, body []byte) error {
	return writeProductStatusMigrationRootFile(root, path, body, productStatusMigrationDigest(body))
}

func writeProductStatusMigrationManifestDurable(root *os.Root, manifest productStatusMigrationManifest) error {
	if err := validateProductStatusMigrationManifest(manifest); err != nil {
		return err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(productStatusMigrationTransactionRelPath, productStatusMigrationManifestFileName)
	writeErr := atomicfile.WriteFileRoot(root, path, append(body, '\n'), 0o600)
	if writeErr == nil {
		return nil
	}
	if !artifactWriteCommitted(writeErr) {
		return writeErr
	}
	written, err := readProductStatusMigrationManifest(root)
	if err != nil {
		return fmt.Errorf("%v; verify migration journal: %w", writeErr, err)
	}
	if !reflect.DeepEqual(written, manifest) {
		return fmt.Errorf("%v; committed migration journal differs from requested manifest", writeErr)
	}
	if err := atomicfile.SyncDirRoot(root, productStatusMigrationTransactionRelPath); err != nil {
		return fmt.Errorf("%v; retry migration journal directory sync: %w", writeErr, err)
	}

	return nil
}

func readProductStatusMigrationManifest(root *os.Root) (productStatusMigrationManifest, error) {
	path := filepath.Join(productStatusMigrationTransactionRelPath, productStatusMigrationManifestFileName)
	body, err := readProductStatusMigrationRootFile(root, path)
	if err != nil {
		return productStatusMigrationManifest{}, err
	}
	var manifest productStatusMigrationManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return productStatusMigrationManifest{}, err
	}
	if err := validateProductStatusMigrationManifest(manifest); err != nil {
		return productStatusMigrationManifest{}, err
	}

	return manifest, nil
}

func validateProductStatusMigrationManifest(manifest productStatusMigrationManifest) error {
	if manifest.SchemaVersion != productStatusMigrationManifestSchema {
		return fmt.Errorf("unsupported product status migration manifest schema %q", manifest.SchemaVersion)
	}
	if manifest.State != productStatusMigrationTransactionPrepared && manifest.State != productStatusMigrationTransactionCommitted {
		return fmt.Errorf("unsupported product status migration manifest state %q", manifest.State)
	}
	for name, value := range map[string]string{
		"previous workflow":  manifest.PreviousWorkflowDigest,
		"candidate workflow": manifest.CandidateWorkflowDigest,
		"previous events":    manifest.PreviousEventsDigest,
		"candidate events":   manifest.CandidateEventsDigest,
		"previous index":     manifest.PreviousIndexDigest,
		"candidate index":    manifest.CandidateIndexDigest,
	} {
		if !validWorkflowArtifactDigest(value) {
			return fmt.Errorf("product status migration manifest has invalid %s digest", name)
		}
	}
	if manifest.PreviousWorkflowDigest == manifest.CandidateWorkflowDigest ||
		manifest.PreviousEventsDigest == manifest.CandidateEventsDigest ||
		manifest.PreviousIndexDigest == manifest.CandidateIndexDigest {
		return fmt.Errorf("product status migration manifest contains an unchanged candidate generation")
	}
	for name, value := range map[string]string{
		"source_folder_path": manifest.SourceFolderPath,
		"target_folder_path": manifest.TargetFolderPath,
		"events_path":        manifest.EventsPath,
	} {
		clean, ok := normalizedSafeEffortPath(value)
		if !ok || clean != filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))) {
			return fmt.Errorf("product status migration %s must be a normalized safe relative path", name)
		}
	}
	if !safeMigrationRelativePath(manifest.SourceFolderPath) || !safeMigrationRelativePath(manifest.TargetFolderPath) || manifest.SourceFolderPath == manifest.TargetFolderPath {
		return fmt.Errorf("product status migration source and target folders must be distinct effort paths")
	}
	if strings.TrimSpace(manifest.EffortID) == "" || strings.TrimSpace(manifest.EventID) == "" {
		return fmt.Errorf("product status migration effort_id and event_id are required")
	}
	if filepath.Base(filepath.FromSlash(manifest.SourceFolderPath)) != manifest.EffortID || filepath.Base(filepath.FromSlash(manifest.TargetFolderPath)) != manifest.EffortID {
		return fmt.Errorf("product status migration folder paths must identify effort %q", manifest.EffortID)
	}
	if err := validateProductWorkflowEventsPathConflicts(manifest.EventsPath, nil); err != nil {
		return fmt.Errorf("product status migration events path: %w", err)
	}

	return nil
}

func readProductStatusMigrationCandidates(root *os.Root, manifest productStatusMigrationManifest) (productStatusMigrationCandidates, error) {
	candidates := productStatusMigrationCandidates{}
	var err error
	candidates.workflow, err = readProductStatusMigrationRootFile(root, filepath.Join(productStatusMigrationTransactionRelPath, productStatusMigrationCandidateWorkflowName))
	if err != nil {
		return candidates, err
	}
	candidates.events, err = readProductStatusMigrationRootFile(root, filepath.Join(productStatusMigrationTransactionRelPath, productStatusMigrationCandidateEventsName))
	if err != nil {
		return candidates, err
	}
	candidates.index, err = readProductStatusMigrationRootFile(root, filepath.Join(productStatusMigrationTransactionRelPath, productStatusMigrationCandidateIndexName))
	if err != nil {
		return candidates, err
	}
	if err := validateProductStatusMigrationCandidates(manifest, candidates); err != nil {
		return candidates, err
	}

	return candidates, nil
}

func validateProductStatusMigrationCandidates(manifest productStatusMigrationManifest, candidates productStatusMigrationCandidates) error {
	for name, candidate := range map[string]struct {
		body   []byte
		digest string
	}{
		"workflow": {body: candidates.workflow, digest: manifest.CandidateWorkflowDigest},
		"events":   {body: candidates.events, digest: manifest.CandidateEventsDigest},
		"index":    {body: candidates.index, digest: manifest.CandidateIndexDigest},
	} {
		if productStatusMigrationDigest(candidate.body) != candidate.digest {
			return fmt.Errorf("product status migration %s candidate digest mismatch", name)
		}
	}
	if err := verifyProductStatusMigrationWorkflow(candidates.workflow, manifest); err != nil {
		return err
	}
	if _, err := productStatusMigrationEventByID(candidates.events, manifest.EventID); err != nil {
		return err
	}
	if _, err := ParseEffortIndexJSON(candidates.index); err != nil {
		return err
	}

	return nil
}

func verifyProductStatusMigrationWorkflow(body []byte, manifest productStatusMigrationManifest) error {
	state, err := ParseWorkflowStateJSON(body)
	if err != nil {
		return err
	}
	if state.EffortID != manifest.EffortID || state.LastEventID != manifest.EventID || !folderPathMatchesRelative(state.FolderPath, manifest.TargetFolderPath) {
		return fmt.Errorf("migration workflow candidate does not match journal authority")
	}
	eventsPath, err := productWorkflowEventsPath(state)
	if err != nil {
		return fmt.Errorf("migration workflow candidate events authority: %w", err)
	}
	if filepath.ToSlash(eventsPath) != manifest.EventsPath {
		return fmt.Errorf("migration workflow candidate events authority does not match journal path")
	}

	return nil
}

func productStatusMigrationEventByID(body []byte, eventID string) (ProductStatusMigrationEvent, error) {
	matches, err := productWorkflowEventEnvelopesByID(body, eventID)
	if err != nil {
		return ProductStatusMigrationEvent{}, fmt.Errorf("parse events: %w", err)
	}
	var found *ProductStatusMigrationEvent
	for _, match := range matches {
		var event ProductStatusMigrationEvent
		if err := json.Unmarshal(match.raw, &event); err != nil {
			return ProductStatusMigrationEvent{}, fmt.Errorf("parse migration event line %d: %w", match.LineNumber, err)
		}
		if found != nil && !reflect.DeepEqual(*found, event) {
			return ProductStatusMigrationEvent{}, fmt.Errorf("migration event_id %q has conflicting ledger entries", eventID)
		}
		copy := event
		found = &copy
	}
	if found == nil {
		return ProductStatusMigrationEvent{}, fmt.Errorf("migration event_id %q is not present", eventID)
	}

	return *found, nil
}

func productStatusMigrationDigest(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func productStatusMigrationDirectoryExists(root *os.Root, path string) (bool, error) {
	info, err := root.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("migration folder %s must be a directory and must not be a symlink", filepath.ToSlash(path))
	}

	return true, nil
}

func inspectProductStatusMigrationFolderGeneration(
	root *os.Root,
	folderPath string,
	manifest productStatusMigrationManifest,
) (string, string, error) {
	workflowPath := filepath.Join(folderPath, WorkflowStateFileName)
	if err := validateEffortArtifactParents(root, workflowPath); err != nil {
		return "", "", err
	}
	body, err := readProductStatusMigrationRootFile(root, workflowPath)
	if err != nil {
		return "", "", err
	}
	digest := productStatusMigrationDigest(body)
	switch digest {
	case manifest.PreviousWorkflowDigest:
		return "previous", digest, nil
	case manifest.CandidateWorkflowDigest:
		return "candidate", digest, nil
	default:
		return "unknown", digest, nil
	}
}

func syncProductStatusMigrationRename(indexRoot *os.Root, sourceRel, targetRel, effortsRoot string) error {
	targetParent := filepath.Dir(targetRel)
	if err := atomicfile.SyncDirRoot(indexRoot, targetParent); err != nil {
		return fmt.Errorf("sync migration target parent: %w", err)
	}
	if err := productStatusMigrationSyncDir(filepath.Join(effortsRoot, targetParent)); err != nil {
		return err
	}
	sourceParent := filepath.Dir(sourceRel)
	if sourceParent != targetParent {
		if err := atomicfile.SyncDirRoot(indexRoot, sourceParent); err != nil {
			return fmt.Errorf("sync migration source parent: %w", err)
		}
		if err := productStatusMigrationSyncDir(filepath.Join(effortsRoot, sourceParent)); err != nil {
			return err
		}
	}

	return nil
}

func recoveredProductStatusMigrationPlan(plan ProductStatusMigrationPlan, recovery productStatusMigrationRecovery) ProductStatusMigrationPlan {
	manifest := recovery.manifest
	plan.Status = ProductWorkflowStatusApplied
	plan.EffortID = manifest.EffortID
	plan.SourceFolderPath = manifest.SourceFolderPath
	plan.TargetFolderPath = manifest.TargetFolderPath
	plan.SourceStatusBucket = firstPathSegment(manifest.SourceFolderPath)
	plan.TargetStatusBucket = firstPathSegment(manifest.TargetFolderPath)
	plan.CommitWarning = "recovered an interrupted product status migration before applying a new mutation"
	plan.NextValidAction = fmt.Sprintf("inspect migrated effort under %s and rerun `idiom product status --effort-root <path>`", manifest.TargetFolderPath)
	if state, err := ParseWorkflowStateJSON(recovery.candidates.workflow); err == nil {
		plan.UpdatedWorkflowState = state
		plan.PriorFolderPaths = append([]string{}, state.PriorFolderPaths...)
	}
	if index, err := ParseEffortIndexJSON(recovery.candidates.index); err == nil {
		plan.UpdatedEffortIndex = index
		for _, entry := range index.Efforts {
			if entry.EffortID == manifest.EffortID {
				plan.UpdatedIndexEntry = entry
				plan.PriorIndexPaths = append([]string{}, entry.PriorPaths...)
				break
			}
		}
	}
	if event, err := productStatusMigrationEventByID(recovery.candidates.events, manifest.EventID); err == nil {
		plan.Event = event
	}

	return plan
}

func committedProductStatusMigrationPlan(plan ProductStatusMigrationPlan, err error) ProductStatusMigrationPlan {
	plan.Status = ProductWorkflowStatusApplied
	plan.CommitWarning = (&atomicfile.PostCommitError{Op: "product status migration folder move committed; forward recovery required", Err: err}).Error()
	plan.NextValidAction = "rerun status migration with the original effort path to recover the journal, then inspect the migrated target"

	return plan
}

func abortProductStatusMigrationBeforeRename(indexRoot, workflowRoot *os.Root, cause error) error {
	markerExists, err := productStatusMigrationPendingMarkerExists(workflowRoot)
	if err != nil {
		return fmt.Errorf("%w (inspect pending migration marker: %v; efforts-root journal retained)", cause, err)
	}
	if markerExists {
		if err := removeProductStatusMigrationPendingMarker(workflowRoot); err != nil {
			return fmt.Errorf("%w (remove pending migration marker: %v; efforts-root journal retained)", cause, err)
		}
	}

	return abortProductStatusMigration(indexRoot, cause)
}

func abortProductStatusMigration(root *os.Root, cause error) error {
	if cleanupErr := removeProductStatusMigrationTransaction(root); cleanupErr != nil {
		return fmt.Errorf("%w (migration transaction cleanup failed: %v)", cause, cleanupErr)
	}

	return cause
}

func removeProductStatusMigrationTransaction(root *os.Root) error {
	info, err := root.Lstat(productStatusMigrationTransactionRelPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("product status migration transaction path must be a directory and must not be a symlink")
	}
	if err := root.RemoveAll(productStatusMigrationTransactionRelPath); err != nil {
		return err
	}

	return atomicfile.SyncDirRoot(root, ".")
}

func loadProductStatusMigrationDiskStateLocked(
	state productStatusMigrationDiskState,
	workflowRoot *os.Root,
	indexRoot *os.Root,
	plan *ProductStatusMigrationPlan,
) (productStatusMigrationDiskState, bool) {
	state.workflowBytes = nil
	state.indexBytes = nil
	state.eventsBytes = nil
	state.workflowState = WorkflowState{}
	state.effortIndex = EffortIndex{}

	indexBytes, err := readProductStatusMigrationRootFile(indexRoot, "index.json")
	if err != nil {
		if os.IsNotExist(err) {
			plan.addBlocker("missing_effort_index", "efforts/index.json", "effort index not found")
		} else {
			plan.addBlocker("read_effort_index_error", "efforts/index.json", err.Error())
		}
	} else {
		state.indexBytes = indexBytes
		index, parseErr := ParseEffortIndexJSON(indexBytes)
		if parseErr != nil {
			plan.addBlocker("malformed_effort_index", "efforts/index.json", parseErr.Error())
		} else {
			state.effortIndex = index
		}
	}

	workflowBytes, err := readProductStatusMigrationRootFile(workflowRoot, WorkflowStateFileName)
	if err != nil {
		if os.IsNotExist(err) {
			plan.addBlocker("missing_workflow_state", WorkflowStateFileName, WorkflowStateFileName+" not found")
		} else {
			plan.addBlocker("read_workflow_state_error", WorkflowStateFileName, err.Error())
		}

		return state, false
	}
	state.workflowBytes = workflowBytes
	workflowState, err := ParseWorkflowStateJSON(workflowBytes)
	if err != nil {
		plan.addBlocker("malformed_workflow_state", WorkflowStateFileName, err.Error())

		return state, false
	}
	state.workflowState = workflowState
	plan.EffortID = workflowState.EffortID
	plan.SourceStatusBucket = workflowState.StatusBucket

	eventsPath, eventsPathErr := productWorkflowEventsPath(workflowState)
	if eventsPathErr != nil {
		plan.addBlocker("invalid_events_artifact_path", WorkflowStateFileName, eventsPathErr.Error())
	} else {
		state.eventsPath = filepath.ToSlash(eventsPath)
		eventsBytes, readErr := readProductStatusMigrationRootFile(workflowRoot, eventsPath)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				plan.addBlocker("missing_events_file", state.eventsPath, "events ledger not found")
			} else {
				plan.addBlocker("read_events_file_error", state.eventsPath, readErr.Error())
			}
		} else {
			state.eventsBytes = eventsBytes
		}
	}

	state.plannerRoot = deriveProductStatusMigrationPlannerRoot(workflowState.FolderPath, state.sourceFolderPath)
	if len(plan.Blockers) > 0 {
		return state, false
	}

	return state, true
}

func readProductStatusMigrationRootFile(root *os.Root, path string) ([]byte, error) {
	if root == nil {
		return nil, fmt.Errorf("migration root is not open")
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("migration path %s must be a regular file and must not be a symlink", filepath.ToSlash(path))
	}

	return root.ReadFile(path)
}

func ensureProductStatusMigrationRootPathAbsent(root *os.Root, path string) error {
	if root == nil {
		return fmt.Errorf("migration root is not open")
	}
	if _, err := root.Lstat(path); err == nil {
		return fmt.Errorf("migration path %s already exists", filepath.ToSlash(path))
	} else if !os.IsNotExist(err) {
		return err
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

	workflowPath := filepath.Join(absEffortRoot, WorkflowStateFileName)
	if err := rejectProductStatusMigrationSymlinks(absEffortRoot, workflowPath); err != nil {
		plan.addBlocker("unsafe_workflow_state_symlink", WorkflowStateFileName, err.Error())

		return state, false
	}

	workflowBytes, workflowErr := os.ReadFile(workflowPath)
	if workflowErr != nil {
		if os.IsNotExist(workflowErr) {
			plan.addBlocker("missing_workflow_state", WorkflowStateFileName, WorkflowStateFileName+" not found")
		} else {
			plan.addBlocker("read_workflow_state_error", WorkflowStateFileName, workflowErr.Error())
		}

		return state, false
	}

	state.workflowBytes = workflowBytes

	workflowState, parseErr := ParseWorkflowStateJSON(workflowBytes)
	if parseErr != nil {
		plan.addBlocker("malformed_workflow_state", WorkflowStateFileName, parseErr.Error())

		return state, false
	}

	state.workflowState = workflowState
	plan.EffortID = workflowState.EffortID
	plan.SourceStatusBucket = workflowState.StatusBucket

	eventsPath, eventsPathErr := productWorkflowEventsPath(workflowState)
	if eventsPathErr != nil {
		plan.addBlocker("invalid_events_artifact_path", WorkflowStateFileName, eventsPathErr.Error())
	} else {
		state.eventsPath = filepath.ToSlash(eventsPath)

		eventsFullPath := filepath.Join(absEffortRoot, eventsPath)

		if err := rejectProductStatusMigrationSymlinks(absEffortRoot, eventsFullPath); err != nil {
			plan.addBlocker("unsafe_events_file_symlink", state.eventsPath, err.Error())
		} else {
			eventsBytes, eventsErr := os.ReadFile(eventsFullPath)
			if eventsErr != nil {
				if os.IsNotExist(eventsErr) {
					plan.addBlocker("missing_events_file", state.eventsPath, "events ledger not found")
				} else {
					plan.addBlocker("read_events_file_error", state.eventsPath, eventsErr.Error())
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

func syncProductStatusMigrationFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return file.Sync()
}

func syncProductStatusMigrationDir(path string) error {
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
