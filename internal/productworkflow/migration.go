package productworkflow

import (
	"fmt"
	"path"
	"strings"
)

const defaultDurableEffortsRoot = "docs/product-workflows/efforts"

type ProductStatusMigrationOptions struct {
	SourceFolderPath     string
	TargetStatusBucket   string
	TargetCurrentState   string
	UpdatedAt            string
	MigrationEventID     string
	WorkspaceFingerprint string
	EffortsRoot          string
}

type ProductStatusMigrationContractInput struct {
	EffortIndexJSON   []byte
	WorkflowStateJSON []byte
	MigrationOptions  ProductStatusMigrationOptions
}

type ProductStatusMigrationPlan struct {
	Status               string                          `json:"status"`
	EffortID             string                          `json:"effort_id"`
	SourceStatusBucket   string                          `json:"source_status_bucket"`
	TargetStatusBucket   string                          `json:"target_status_bucket"`
	SourceFolderPath     string                          `json:"source_folder_path"`
	TargetFolderPath     string                          `json:"target_folder_path"`
	PriorFolderPaths     []string                        `json:"prior_folder_paths"`
	UpdatedWorkflowState WorkflowState                   `json:"updated_workflow_state"`
	UpdatedEffortIndex   EffortIndex                     `json:"updated_effort_index"`
	UpdatedIndexEntry    EffortIndexEntry                `json:"updated_index_entry"`
	Event                ProductStatusMigrationEvent     `json:"event"`
	Blockers             []ProductStatusMigrationBlocker `json:"blockers"`
	NextValidAction      string                          `json:"next_valid_action"`
}

type ProductStatusMigrationEvent struct {
	Event            string `json:"event"`
	EventID          string `json:"event_id"`
	EffortID         string `json:"effort_id"`
	FromStatusBucket string `json:"from_status_bucket"`
	ToStatusBucket   string `json:"to_status_bucket"`
	FromFolderPath   string `json:"from_folder_path"`
	ToFolderPath     string `json:"to_folder_path"`
	StateRevision    int    `json:"state_revision"`
	Timestamp        string `json:"timestamp"`
}

type ProductStatusMigrationBlocker struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func PlanProductStatusMigration(workflowState WorkflowState, effortIndex EffortIndex, options ProductStatusMigrationOptions) ProductStatusMigrationPlan {
	sourceFolder, sourceFolderSafe := normalizedSafeEffortPath(options.SourceFolderPath)
	targetBucket := strings.ToLower(strings.TrimSpace(options.TargetStatusBucket))
	targetState := strings.TrimSpace(options.TargetCurrentState)
	effortsRootValue := options.EffortsRoot
	if strings.TrimSpace(effortsRootValue) == "" {
		effortsRootValue = defaultDurableEffortsRoot
	}
	effortsRoot, effortsRootSafe := normalizedSafeEffortPath(effortsRootValue)
	if effortsRoot == "" && effortsRootSafe {
		effortsRoot = defaultDurableEffortsRoot
	}

	plan := ProductStatusMigrationPlan{
		Status:             "blocked",
		EffortID:           strings.TrimSpace(workflowState.EffortID),
		SourceStatusBucket: strings.TrimSpace(workflowState.StatusBucket),
		TargetStatusBucket: targetBucket,
		SourceFolderPath:   sourceFolder,
		Blockers:           []ProductStatusMigrationBlocker{},
		NextValidAction:    "resolve status migration blockers before applying a migration",
	}

	if strings.TrimSpace(workflowState.EffortID) == "" {
		plan.addBlocker("missing_effort_id", "workflow_state.json", "workflow_state missing effort_id")
	}
	if strings.TrimSpace(options.UpdatedAt) == "" {
		plan.addBlocker("missing_migration_timestamp", "updated_at", "status migration plan requires an updated_at timestamp")
	}
	if strings.TrimSpace(options.MigrationEventID) == "" {
		plan.addBlocker("missing_migration_event_id", "migration_event_id", "status migration plan requires a migration event id")
	}
	if !sourceFolderSafe {
		plan.addBlocker("unsafe_source_folder_path", "source_folder_path", "source folder path must be a safe relative effort path")
	}
	if !effortsRootSafe {
		plan.addBlocker("unsafe_efforts_root", "efforts_root", "efforts root must be a safe relative path")
	}
	if !validStatusBuckets[workflowState.StatusBucket] {
		plan.addBlocker("invalid_source_status_bucket", "workflow_state.json", fmt.Sprintf("source status_bucket %q is not valid", workflowState.StatusBucket))
	}
	if !validStatusBuckets[targetBucket] {
		plan.addBlocker("invalid_target_status_bucket", "target_status_bucket", fmt.Sprintf("target status_bucket %q is not valid", targetBucket))
	}
	if workflowState.StatusBucket == targetBucket && targetBucket != "" {
		plan.addBlocker("noop_status_migration", "target_status_bucket", "target status bucket must differ from source status bucket")
	}
	if _, ok := validStates[workflowState.CurrentState]; !ok && workflowState.CurrentState != "" {
		plan.addBlocker("invalid_current_state", "workflow_state.json", fmt.Sprintf("current_state %q is not valid", workflowState.CurrentState))
	}
	if consistency := validateStateBucketConsistency(workflowState.StatusBucket, workflowState.CurrentState); consistency != nil {
		plan.addBlocker(consistency.Code, "workflow_state.json", consistency.Message)
	}
	if targetState == "" {
		targetState = defaultMigrationTargetState(targetBucket, workflowState.CurrentState)
	}
	if consistency := validateStateBucketConsistency(targetBucket, targetState); consistency != nil {
		plan.addBlocker("invalid_target_state_bucket", "target_current_state", consistency.Message)
	}

	sourceBucket := firstPathSegment(sourceFolder)
	if sourceBucket != "" && workflowState.StatusBucket != "" && sourceBucket != workflowState.StatusBucket {
		plan.addBlocker("source_folder_bucket_mismatch", "source_folder_path", fmt.Sprintf("source folder bucket %q does not match workflow status_bucket %q", sourceBucket, workflowState.StatusBucket))
	}
	workflowFolderPath := ""
	if strings.TrimSpace(workflowState.FolderPath) != "" {
		var workflowFolderPathSafe bool
		workflowFolderPath, workflowFolderPathSafe = normalizedSafeEffortPath(workflowState.FolderPath)
		if !workflowFolderPathSafe {
			plan.addBlocker("unsafe_workflow_folder_path", "workflow_state.json", fmt.Sprintf("workflow folder_path %q is not a safe relative path", publicEffortPath(workflowState.FolderPath)))
		}
	}
	for _, priorPath := range workflowState.PriorFolderPaths {
		if _, ok := normalizedSafeEffortPath(priorPath); !ok {
			plan.addBlocker("unsafe_workflow_prior_folder_path", "workflow_state.json", fmt.Sprintf("workflow prior_folder_paths contains %q, which is not a safe relative path", publicEffortPath(priorPath)))
		}
	}
	if sourceFolder != "" && workflowFolderPath != "" && !folderPathMatchesRelative(workflowFolderPath, sourceFolder) {
		plan.addBlocker("workflow_folder_path_mismatch", "workflow_state.json", fmt.Sprintf("workflow folder_path %q does not match source folder %q", publicEffortPath(workflowState.FolderPath), sourceFolder))
	}
	if sourceFolder != "" && workflowState.EffortID != "" && path.Base(sourceFolder) != workflowState.EffortID {
		plan.addBlocker("effort_id_folder_mismatch", "source_folder_path", fmt.Sprintf("source folder basename %q does not match effort_id %q", path.Base(sourceFolder), workflowState.EffortID))
	}

	indexEntry, indexBlockers := migrationIndexEntry(effortIndex, workflowState.EffortID)
	for _, blocker := range indexBlockers {
		plan.Blockers = append(plan.Blockers, blocker)
	}
	if indexEntry != nil {
		indexFolderPath := ""
		if strings.TrimSpace(indexEntry.FolderPath) != "" {
			var indexFolderPathSafe bool
			indexFolderPath, indexFolderPathSafe = normalizedSafeEffortPath(indexEntry.FolderPath)
			if !indexFolderPathSafe {
				plan.addBlocker("unsafe_index_folder_path", "efforts/index.json", fmt.Sprintf("index folder_path %q is not a safe relative path", publicEffortPath(indexEntry.FolderPath)))
			}
		}
		for _, priorPath := range indexEntry.PriorPaths {
			if _, ok := normalizedSafeEffortPath(priorPath); !ok {
				plan.addBlocker("unsafe_index_prior_path", "efforts/index.json", fmt.Sprintf("index prior_paths contains %q, which is not a safe relative path", publicEffortPath(priorPath)))
			}
		}
		if indexFolderPath != "" && sourceFolder != "" && indexFolderPath != sourceFolder {
			plan.addBlocker("index_folder_path_mismatch", "efforts/index.json", fmt.Sprintf("index folder_path %q does not match source folder %q", publicEffortPath(indexEntry.FolderPath), sourceFolder))
		}
		if indexEntry.StatusBucket != "" && workflowState.StatusBucket != "" && indexEntry.StatusBucket != workflowState.StatusBucket {
			plan.addBlocker("index_status_bucket_mismatch", "efforts/index.json", fmt.Sprintf("index status_bucket %q does not match workflow status_bucket %q", indexEntry.StatusBucket, workflowState.StatusBucket))
		}
	}

	if len(plan.Blockers) > 0 {
		return plan
	}

	targetFolder := normalizeEffortRelPath(path.Join(targetBucket, workflowState.EffortID))
	updatedWorkflow := workflowState
	updatedWorkflow.StatusBucket = targetBucket
	updatedWorkflow.CurrentState = targetState
	updatedWorkflow.UpdatedAt = strings.TrimSpace(options.UpdatedAt)
	updatedWorkflow.StateRevision = workflowState.StateRevision + 1
	updatedWorkflow.LastEventID = strings.TrimSpace(options.MigrationEventID)
	if strings.TrimSpace(options.WorkspaceFingerprint) != "" {
		updatedWorkflow.WorkspaceFingerprint = strings.TrimSpace(options.WorkspaceFingerprint)
	}
	updatedWorkflow.FolderPath = path.Join(effortsRoot, targetFolder)
	updatedWorkflow.PriorFolderPaths = appendUniqueStrings(compactSafeEffortPaths(updatedWorkflow.PriorFolderPaths), workflowFolderPath)

	updatedIndexEntry := *indexEntry
	updatedIndexEntry.StatusBucket = targetBucket
	updatedIndexEntry.CurrentState = targetState
	updatedIndexEntry.CurrentPhase = workflowState.CurrentPhase
	updatedIndexEntry.TaskID = workflowState.TaskID
	updatedIndexEntry.Title = workflowState.Title
	updatedIndexEntry.FolderPath = targetFolder
	updatedIndexEntry.PriorPaths = appendUniqueStrings(compactSafeEffortPaths(updatedIndexEntry.PriorPaths), sourceFolder)
	updatedIndex := updatedEffortIndex(effortIndex, workflowState.EffortID, updatedIndexEntry, strings.TrimSpace(options.UpdatedAt))

	plan.Status = "planned"
	plan.TargetFolderPath = targetFolder
	plan.PriorFolderPaths = append([]string{}, updatedIndexEntry.PriorPaths...)
	plan.UpdatedWorkflowState = updatedWorkflow
	plan.UpdatedEffortIndex = updatedIndex
	plan.UpdatedIndexEntry = updatedIndexEntry
	plan.Event = ProductStatusMigrationEvent{
		Event:            "status_migration_planned",
		EventID:          strings.TrimSpace(options.MigrationEventID),
		EffortID:         workflowState.EffortID,
		FromStatusBucket: workflowState.StatusBucket,
		ToStatusBucket:   targetBucket,
		FromFolderPath:   sourceFolder,
		ToFolderPath:     targetFolder,
		StateRevision:    updatedWorkflow.StateRevision,
		Timestamp:        strings.TrimSpace(options.UpdatedAt),
	}
	plan.NextValidAction = "review the status migration plan before applying any folder move"
	return plan
}

func PlanProductStatusMigrationContract(input ProductStatusMigrationContractInput) ProductStatusMigrationPlan {
	idx, idxErr := ParseEffortIndexJSON(input.EffortIndexJSON)
	if idxErr != nil {
		return ProductStatusMigrationPlan{
			Status: "blocked",
			Blockers: []ProductStatusMigrationBlocker{
				{
					Code:    "malformed_effort_index",
					Path:    "efforts/index.json",
					Message: idxErr.Error(),
				},
			},
			NextValidAction: "fix effort index JSON before planning status migration",
		}
	}

	ws, wsErr := ParseWorkflowStateJSON(input.WorkflowStateJSON)
	if wsErr != nil {
		return ProductStatusMigrationPlan{
			Status: "blocked",
			Blockers: []ProductStatusMigrationBlocker{
				{
					Code:    "malformed_workflow_state",
					Path:    "workflow_state.json",
					Message: wsErr.Error(),
				},
			},
			NextValidAction: "fix workflow state JSON before planning status migration",
		}
	}

	options := input.MigrationOptions
	if strings.TrimSpace(options.SourceFolderPath) == "" {
		if entry, blockers := migrationIndexEntry(idx, ws.EffortID); len(blockers) == 0 && entry != nil {
			options.SourceFolderPath = entry.FolderPath
		}
	}
	return PlanProductStatusMigration(ws, idx, options)
}

func defaultMigrationTargetState(targetBucket, currentState string) string {
	switch targetBucket {
	case "unstarted":
		return "planned"
	case "closed":
		return "closed"
	case "started":
		if currentState == "" || currentState == "closed" {
			return "planned"
		}
		return currentState
	default:
		return currentState
	}
}

func migrationIndexEntry(index EffortIndex, effortID string) (*EffortIndexEntry, []ProductStatusMigrationBlocker) {
	if strings.TrimSpace(effortID) == "" {
		return nil, nil
	}
	var matches []EffortIndexEntry
	for _, entry := range index.Efforts {
		if entry.EffortID == effortID {
			matches = append(matches, entry)
		}
	}
	switch len(matches) {
	case 0:
		return nil, []ProductStatusMigrationBlocker{{
			Code:    "missing_effort_index_entry",
			Path:    "efforts/index.json",
			Message: fmt.Sprintf("effort index missing entry for effort_id %q", effortID),
		}}
	case 1:
		return &matches[0], nil
	default:
		return nil, []ProductStatusMigrationBlocker{{
			Code:    "duplicate_effort_index_entry",
			Path:    "efforts/index.json",
			Message: fmt.Sprintf("effort index has %d entries for effort_id %q", len(matches), effortID),
		}}
	}
}

func updatedEffortIndex(index EffortIndex, effortID string, updatedEntry EffortIndexEntry, updatedAt string) EffortIndex {
	updated := index
	updated.UpdatedAt = updatedAt
	updated.Efforts = append([]EffortIndexEntry{}, index.Efforts...)
	for i := range updated.Efforts {
		if updated.Efforts[i].EffortID == effortID {
			updated.Efforts[i] = updatedEntry
			break
		}
	}
	return updated
}

func safeEffortRelativePath(value string) bool {
	_, ok := normalizedSafeEffortPath(value)
	return ok
}

func normalizedSafeEffortPath(value string) (string, bool) {
	raw := strings.TrimSpace(filepathToSlash(value))
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, ":") {
		return "", false
	}
	trimmed := strings.TrimRight(raw, "/")
	if trimmed == "" {
		return "", false
	}
	cleaned := path.Clean(trimmed)
	if cleaned != trimmed {
		return "", false
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return cleaned, true
}

func normalizeEffortRelPath(value string) string {
	value = strings.TrimSpace(filepathToSlash(value))
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	return path.Clean(value)
}

func filepathToSlash(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

func firstPathSegment(value string) string {
	value = normalizeEffortRelPath(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "/"); idx >= 0 {
		return value[:idx]
	}
	return value
}

func folderPathMatchesRelative(folderPath, relPath string) bool {
	folderPath = normalizeEffortRelPath(folderPath)
	relPath = normalizeEffortRelPath(relPath)
	return folderPath == relPath || strings.HasSuffix(folderPath, "/"+relPath)
}

func publicEffortPath(value string) string {
	if normalized, ok := normalizedSafeEffortPath(value); ok {
		return normalized
	}
	raw := strings.TrimSpace(filepathToSlash(value))
	if strings.HasPrefix(raw, "/") {
		return "<absolute-path>"
	}
	return "<unsafe-path>"
}

func compactSafeEffortPaths(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if normalized, ok := normalizedSafeEffortPath(value); ok {
			out = append(out, normalized)
		}
	}
	return out
}

func appendUniqueStrings(values []string, next string) []string {
	next = strings.TrimSpace(next)
	if next == "" {
		return values
	}
	for _, value := range values {
		if value == next {
			return values
		}
	}
	return append(values, next)
}

func (plan *ProductStatusMigrationPlan) addBlocker(code, path, message string) {
	plan.Blockers = append(plan.Blockers, ProductStatusMigrationBlocker{
		Code:    code,
		Path:    path,
		Message: message,
	})
}
