package routeprobe

import (
	"errors"
	"fmt"
	"strings"

	"idiom/internal/contracts/autonomy"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/runtime/workerruntime"
)

const explicitTaskFamilyReason = "explicit_task_family"

// LaneOptions configures one synthetic-brief route probe. The probe takes the
// same inputs the lane wrapper sets when forcing a task family explicitly and
// resolves the route/brief shape without spinning up a model session.
type LaneOptions struct {
	TaskFamily   string
	AllowedTools []string
}

// LaneResult captures the route plan and runtime brief shape produced by the
// probe. Field names mirror route_plan telemetry so operators can correlate
// probe output with session logs.
type LaneResult struct {
	InitialLane   string
	FollowupLane  string
	Reason        string
	Contract      string
	BriefTaskFam  string
	AllowedToolsN int
}

func Lane(opts LaneOptions) (LaneResult, error) {
	family := strings.TrimSpace(opts.TaskFamily)
	if family == "" {
		return LaneResult{}, errors.New("probe: TaskFamily is required")
	}

	result := explicitTaskFamilyLaneResult(family, opts.AllowedTools)
	if result.InitialLane == "" {
		return LaneResult{}, fmt.Errorf("probe: unsupported explicit TaskFamily %q", family)
	}

	return result, nil
}

func explicitTaskFamilyLaneResult(family string, allowedTools []string) LaneResult {
	taskFamily := autonomy.TaskFamilyID(family)
	if !autonomy.TaskFamilyUsesImplementationLane(taskFamily) {
		return LaneResult{}
	}

	return LaneResult{
		InitialLane:   string(lanecontract.LaneImplement),
		Reason:        explicitTaskFamilyReason,
		Contract:      string(workerruntime.ContractImplementationResult),
		BriefTaskFam:  family,
		AllowedToolsN: len(allowedTools),
	}
}

func FormatResult(label string, r LaneResult) string {
	return fmt.Sprintf("%s initial=%s followup=%s reason=%s contract=%s task_family=%s tools=%d",
		label, dashIfEmpty(r.InitialLane), dashIfEmpty(r.FollowupLane),
		dashIfEmpty(r.Reason), dashIfEmpty(r.Contract),
		dashIfEmpty(r.BriefTaskFam), r.AllowedToolsN)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}

	return s
}
