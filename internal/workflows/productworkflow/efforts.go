package productworkflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultProductEffortsRoot = "docs/product-workflows/efforts"

type ProductEffortsOptions struct {
	Root   string
	Status string
	Query  string
}

type ProductEffortsResult struct {
	Status          string                 `json:"status"`
	Root            string                 `json:"root"`
	StatusFilter    string                 `json:"status_filter"`
	Query           string                 `json:"query,omitempty"`
	Efforts         []ProductEffortSummary `json:"efforts"`
	Findings        []ProductEffortFinding `json:"findings"`
	Counts          ProductEffortsCounts   `json:"counts"`
	NextValidAction string                 `json:"next_valid_action"`
}

type ProductEffortSummary struct {
	EffortID            string   `json:"effort_id"`
	Title               string   `json:"title"`
	StatusBucket        string   `json:"status_bucket"`
	CurrentPhase        string   `json:"current_phase"`
	CurrentState        string   `json:"current_state"`
	TaskID              string   `json:"task_id"`
	StateRevision       int      `json:"state_revision"`
	LastEventID         string   `json:"last_event_id"`
	ContextPackID       string   `json:"context_pack_id,omitempty"`
	FreshnessState      string   `json:"freshness_state,omitempty"`
	BlockerCount        int      `json:"blocker_count"`
	OpenObligationCount int      `json:"open_obligation_count"`
	NextValidActions    []string `json:"next_valid_actions"`
	RelativeFolderPath  string   `json:"relative_folder_path"`
}

type ProductEffortFinding struct {
	ID           string `json:"id"`
	Code         string `json:"code"`
	StatusBucket string `json:"status_bucket,omitempty"`
	Path         string `json:"path"`
	Message      string `json:"message"`
}

type ProductEffortsCounts struct {
	EffortCount  int `json:"effort_count"`
	FindingCount int `json:"finding_count"`
	Unstarted    int `json:"unstarted"`
	Started      int `json:"started"`
	Closed       int `json:"closed"`
}

var productEffortStatusOrder = []string{
	WorkflowStatusBucketUnstarted,
	WorkflowStatusBucketStarted,
	WorkflowStatusBucketClosed,
}

func BrowseProductEfforts(options ProductEffortsOptions) (ProductEffortsResult, error) {
	statusFilter := strings.ToLower(strings.TrimSpace(options.Status))
	if statusFilter == "" {
		statusFilter = "all"
	}

	root := strings.TrimSpace(options.Root)
	if root == "" {
		root = DefaultProductEffortsRoot
	}

	result := ProductEffortsResult{
		Status:       ProductWorkflowStatusPass,
		StatusFilter: statusFilter,
		Query:        strings.TrimSpace(options.Query),
		Efforts:      []ProductEffortSummary{},
		Findings:     []ProductEffortFinding{},
	}

	buckets, err := productEffortBucketsForFilter(statusFilter)
	if err != nil {
		result.Status = ProductWorkflowStatusFail
		result.NextValidAction = "rerun with --status unstarted, started, closed, or all"
		result.addFinding("invalid_status_filter", "", "", err.Error())
		result.refreshCounts()

		return result, err
	}

	absRoot, err := resolveProductEffortsRoot(root)
	if err != nil {
		result.Status = ProductWorkflowStatusFail
		result.NextValidAction = "rerun with a safe existing efforts root"
		result.addFinding("unsafe_efforts_root", "", root, err.Error())
		result.refreshCounts()

		return result, err
	}

	result.Root = filepath.ToSlash(absRoot)

	query := strings.ToLower(result.Query)

	for _, bucket := range buckets {
		bucketPath := filepath.Join(absRoot, bucket)

		entries, err := os.ReadDir(bucketPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			result.addFinding("read_status_bucket_error", bucket, productEffortRelPath(absRoot, bucketPath), err.Error())

			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			effortPath := filepath.Join(bucketPath, entry.Name())
			relPath := productEffortRelPath(absRoot, effortPath)
			wsPath := filepath.Join(effortPath, WorkflowStateFileName)

			data, err := os.ReadFile(wsPath)
			if err != nil {
				code := "read_workflow_state_error"
				message := err.Error()

				if os.IsNotExist(err) {
					code = "missing_workflow_state"
					message = WorkflowStateFileName + " not found"
				}

				result.addFinding(code, bucket, filepath.ToSlash(filepath.Join(relPath, WorkflowStateFileName)), message)

				continue
			}

			summary, err := productEffortSummaryFromState(data, bucket, relPath)
			if err != nil {
				result.addFinding("invalid_workflow_state", bucket, filepath.ToSlash(filepath.Join(relPath, WorkflowStateFileName)), err.Error())

				continue
			}

			if query != "" && !productEffortSummaryMatches(summary, query) {
				continue
			}

			result.Efforts = append(result.Efforts, summary)
		}
	}

	sortProductEfforts(result.Efforts)
	sortProductEffortFindings(result.Findings)
	result.refreshCounts()

	if len(result.Findings) > 0 {
		result.Status = ProductWorkflowStatusFail
		result.NextValidAction = "fix product effort workflow_state findings before relying on the effort browser"
	} else if len(result.Efforts) == 0 {
		result.NextValidAction = "adjust --status or --query to select an existing effort"
	} else {
		result.NextValidAction = "select an effort and run idiom product status --effort-root <relative_folder_path>"
	}

	return result, nil
}

func productEffortBucketsForFilter(statusFilter string) ([]string, error) {
	switch statusFilter {
	case "all":
		return append([]string{}, productEffortStatusOrder...), nil
	case WorkflowStatusBucketUnstarted, WorkflowStatusBucketStarted, WorkflowStatusBucketClosed:
		return []string{statusFilter}, nil
	default:
		return nil, fmt.Errorf("unknown status filter %q", statusFilter)
	}
}

func resolveProductEffortsRoot(root string) (string, error) {
	if hasPathTraversal(root) {
		return "", errors.New("efforts root must not contain traversal components")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve efforts root: %w", err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		return "", fmt.Errorf("stat efforts root: %w", err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("efforts root is not a directory: %s", filepath.ToSlash(absRoot))
	}

	return absRoot, nil
}

func hasPathTraversal(path string) bool {
	normalized := strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
	for _, part := range strings.FieldsFunc(normalized, func(r rune) bool { return r == '/' }) {
		if part == ".." {
			return true
		}
	}

	return false
}

func productEffortSummaryFromState(data []byte, bucket string, relPath string) (ProductEffortSummary, error) {
	ws, err := ParseWorkflowStateJSON(data)
	if err != nil {
		return ProductEffortSummary{}, err
	}

	if err := validateProductEffortWorkflowState(ws, bucket); err != nil {
		return ProductEffortSummary{}, err
	}

	if err := validateProductEffortFolderPath(ws, relPath); err != nil {
		return ProductEffortSummary{}, err
	}

	return ProductEffortSummary{
		EffortID:            strings.TrimSpace(ws.EffortID),
		Title:               strings.TrimSpace(ws.Title),
		StatusBucket:        strings.TrimSpace(ws.StatusBucket),
		CurrentPhase:        strings.TrimSpace(ws.CurrentPhase),
		CurrentState:        strings.TrimSpace(ws.CurrentState),
		TaskID:              strings.TrimSpace(ws.TaskID),
		StateRevision:       ws.StateRevision,
		LastEventID:         strings.TrimSpace(ws.LastEventID),
		ContextPackID:       strings.TrimSpace(ws.Context.ContextPackID),
		FreshnessState:      strings.TrimSpace(ws.Context.FreshnessState),
		BlockerCount:        len(ws.Blockers),
		OpenObligationCount: len(ws.OpenObligations),
		NextValidActions:    compactStrings(ws.NextValidActions),
		RelativeFolderPath:  relPath,
	}, nil
}

func validateProductEffortWorkflowState(ws WorkflowState, bucket string) error {
	var errs []error

	if ws.StatusBucket != "" {
		if !validStatusBuckets[ws.StatusBucket] {
			errs = append(errs, fmt.Errorf("workflow state invalid status_bucket %q", ws.StatusBucket))
		} else if ws.StatusBucket != bucket {
			errs = append(errs, fmt.Errorf("workflow state status_bucket %q does not match folder bucket %q", ws.StatusBucket, bucket))
		}
	}

	if _, ok := validStates[ws.CurrentState]; !ok && ws.CurrentState != "" {
		errs = append(errs, fmt.Errorf("workflow state current_state %q is not valid", ws.CurrentState))
	}

	if consistencyBlocker := validateStateBucketConsistency(ws.StatusBucket, ws.CurrentState); consistencyBlocker != nil {
		errs = append(errs, errors.New(consistencyBlocker.Message))
	}

	return errors.Join(errs...)
}

func validateProductEffortFolderPath(ws WorkflowState, relPath string) error {
	folderPath := strings.Trim(strings.TrimSpace(ws.FolderPath), "/")
	if folderPath == "" {
		return nil
	}

	relPath = strings.Trim(strings.TrimSpace(filepath.ToSlash(relPath)), "/")
	if relPath == "" || folderPath == relPath || strings.HasSuffix(folderPath, "/"+relPath) {
		return nil
	}

	return fmt.Errorf("workflow state folder_path %q does not match effort folder %q", ws.FolderPath, relPath)
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}

	return out
}

func productEffortSummaryMatches(summary ProductEffortSummary, query string) bool {
	fields := []string{
		summary.EffortID,
		summary.Title,
		summary.StatusBucket,
		summary.CurrentPhase,
		summary.CurrentState,
		summary.TaskID,
		summary.RelativeFolderPath,
		summary.ContextPackID,
		summary.FreshnessState,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}

	for _, action := range summary.NextValidActions {
		if strings.Contains(strings.ToLower(action), query) {
			return true
		}
	}

	return false
}

func productEffortRelPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}

	return filepath.ToSlash(rel)
}

func sortProductEfforts(efforts []ProductEffortSummary) {
	sort.Slice(efforts, func(i, j int) bool {
		leftBucket := productEffortBucketRank(efforts[i].StatusBucket)
		rightBucket := productEffortBucketRank(efforts[j].StatusBucket)

		if leftBucket != rightBucket {
			return leftBucket < rightBucket
		}

		leftID := efforts[i].EffortID
		if leftID == "" {
			leftID = efforts[i].RelativeFolderPath
		}

		rightID := efforts[j].EffortID
		if rightID == "" {
			rightID = efforts[j].RelativeFolderPath
		}

		if leftID != rightID {
			return leftID < rightID
		}

		return efforts[i].RelativeFolderPath < efforts[j].RelativeFolderPath
	})
}

func sortProductEffortFindings(findings []ProductEffortFinding) {
	sort.Slice(findings, func(i, j int) bool {
		leftBucket := productEffortBucketRank(findings[i].StatusBucket)
		rightBucket := productEffortBucketRank(findings[j].StatusBucket)

		if leftBucket != rightBucket {
			return leftBucket < rightBucket
		}

		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}

		return findings[i].Code < findings[j].Code
	})
}

func productEffortBucketRank(bucket string) int {
	for i, candidate := range productEffortStatusOrder {
		if bucket == candidate {
			return i
		}
	}

	return len(productEffortStatusOrder)
}

func (result *ProductEffortsResult) addFinding(code, bucket, path, message string) {
	if path == "" {
		path = "."
	}

	result.Findings = append(result.Findings, ProductEffortFinding{
		ID:           code + ":" + path,
		Code:         code,
		StatusBucket: bucket,
		Path:         filepath.ToSlash(path),
		Message:      message,
	})
}

func (result *ProductEffortsResult) refreshCounts() {
	counts := ProductEffortsCounts{
		EffortCount:  len(result.Efforts),
		FindingCount: len(result.Findings),
	}
	for _, effort := range result.Efforts {
		switch effort.StatusBucket {
		case WorkflowStatusBucketUnstarted:
			counts.Unstarted++
		case WorkflowStatusBucketStarted:
			counts.Started++
		case WorkflowStatusBucketClosed:
			counts.Closed++
		}
	}

	result.Counts = counts
}
