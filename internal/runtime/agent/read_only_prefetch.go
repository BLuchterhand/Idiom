package agent

import (
	"encoding/json"
	"os"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/workerruntime"
)

const readOnlyPrefetchEventRole = "read_only_named_path_prefetch"

type readOnlyPrefetchEvent struct {
	SchemaVersion            string                          `json:"schema_version"`
	Eligible                 bool                            `json:"eligible"`
	MissReason               string                          `json:"miss_reason,omitempty"`
	RequestedPaths           int                             `json:"requested_paths"`
	SelectedPaths            []string                        `json:"selected_paths,omitempty"`
	SkippedPaths             []workspace.PrefetchSkippedPath `json:"skipped_paths,omitempty"`
	DuplicatePaths           int                             `json:"duplicate_paths,omitempty"`
	Bytes                    int                             `json:"bytes,omitempty"`
	Lines                    int                             `json:"lines,omitempty"`
	TruncatedFiles           int                             `json:"truncated_files,omitempty"`
	EstimatedRoundTripsSaved int                             `json:"estimated_round_trips_saved,omitempty"`
}

func (r *Runner) maybeApplyReadOnlyNamedPathPrefetch(recorder runnerTurnRecorder, collectedResults []workspace.ToolResult) []workspace.ToolResult {
	event := r.readOnlyNamedPathPrefetchEvent(collectedResults)
	defer func() {
		if data, err := json.Marshal(event); err == nil {
			r.emitEvent(RunEvent{Role: readOnlyPrefetchEventRole, Content: string(data)})
		}
	}()
	if !event.Eligible || r == nil || r.Workspace == nil {
		return collectedResults
	}
	paths := readOnlyPrefetchObjectivePaths(r.RuntimeBrief)
	event.RequestedPaths = len(paths)
	if len(paths) == 0 {
		event.Eligible = false
		event.MissReason = "no_explicit_named_paths"
		return collectedResults
	}
	report := r.Workspace.PrefetchReadFiles(paths, workspace.PrefetchReadOptions{})
	event.SelectedPaths = append([]string(nil), report.SelectedPaths...)
	event.SkippedPaths = append([]workspace.PrefetchSkippedPath(nil), report.SkippedPaths...)
	event.DuplicatePaths = report.DuplicatePaths
	event.Bytes = report.Bytes
	event.Lines = report.Lines
	event.TruncatedFiles = report.TruncatedFiles
	event.EstimatedRoundTripsSaved = report.EstimatedRoundTripsSaved
	if len(report.Results) == 0 {
		event.Eligible = false
		event.MissReason = "no_paths_prefetched"
		return collectedResults
	}
	for i, result := range report.Results {
		req := workspace.ToolRequest{Tool: workspace.ToolNamePrefetch, Path: report.SelectedPaths[i]}
		recorder.recordWithOptions(0, req, result, "read_only_named_path_prefetch", runnerRecordOptions{
			TrackProgress: true,
			ResetRejected: true,
		})
	}
	return *recorder.collectedResults
}

func (r *Runner) readOnlyNamedPathPrefetchEvent(collectedResults []workspace.ToolResult) readOnlyPrefetchEvent {
	event := readOnlyPrefetchEvent{SchemaVersion: "idiom.read_only_named_path_prefetch.v1"}
	if r == nil || r.Workspace == nil {
		event.MissReason = "workspace_unavailable"
		return event
	}
	if os.Getenv("IDIOM_DISABLE_READONLY_PREFETCH") == "1" {
		event.MissReason = "disabled_by_env"
		return event
	}
	if readOnlyNamedPathPrefetchChildAgent(r.agentName()) {
		event.MissReason = "delegated_child_agent"
		return event
	}
	if len(collectedResults) > 0 {
		event.MissReason = "existing_evidence_present"
		return event
	}
	if !readOnlyNamedPathPrefetchLane(r.RuntimeBrief) {
		event.MissReason = "not_read_only_workspace"
		return event
	}
	event.Eligible = true
	return event
}

func readOnlyNamedPathPrefetchLane(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	return workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile)
}

// readOnlyNamedPathPrefetchChildAgent keeps delegated child packet production
// model-directed. Child runners have their own evidence-acquisition contract
// and already receive named paths through child-prompt narrowing, so top-level
// prefetch should not alter that actor boundary.
func readOnlyNamedPathPrefetchChildAgent(agentName string) bool {
	agentName = strings.TrimSpace(agentName)
	return strings.HasPrefix(agentName, "child:") || strings.HasPrefix(agentName, "child_write:")
}

func readOnlyPrefetchObjectivePaths(brief workerruntime.WorkerRuntimeBrief) []string {
	text := strings.TrimSpace(brief.Objective)
	if text == "" {
		return nil
	}
	return pathparse.MentionedWorkspacePaths(text)
}
