package app

import (
	"errors"
	"fmt"
	"strings"

	"idiom/internal/runtime/modepolicy"
)

// ProbeLaneOptions configures one synthetic-brief route probe. The
// probe takes the same inputs the lane wrapper sets when forcing a
// task family explicitly (`forcedTaskFamily`) and runs them through
// the route normalizer + runtime-brief builder without spinning up
// a model session — so it's fast enough to wire into pre-merge
// checks, not just operator workflows.
type ProbeLaneOptions struct {
	// Mode is the agent mode the lane runs under. Defaults to
	// modepolicy.BuildMode() when unset.
	Mode modepolicy.Mode
	// TaskFamily is the family the lane wrapper would force via
	// --task-family. Use "implementation" for `idiom implement`-style
	// lanes and "plan_then_execute_planner" for the planner phase of
	// `idiom plan-execute`.
	TaskFamily string
	// AllowedTools is the workspace's tool surface. Production
	// callers pass `workspace.NewManager(cfg).AllowedTools()`; tests
	// pass a stub list. Empty lists are an explicit failure signal.
	AllowedTools []string
}

// ProbeLaneResult captures the route plan and runtime brief shape
// produced by the probe. Field names mirror what `idiom` logs at
// `route_plan` so the operator can correlate probe output with the
// session telemetry they already know how to read.
type ProbeLaneResult struct {
	InitialLane   string
	FollowupLane  string
	Reason        string
	Contract      string
	BriefTaskFam  string
	AllowedToolsN int
}

// ProbeLane runs one synthetic route probe. Returns the resolved
// shape so the caller can assert against expectations. An error
// only signals an invalid input (e.g. empty TaskFamily); the route
// itself never errors — callers compare the result against their
// expected shape.
func ProbeLane(opts ProbeLaneOptions) (ProbeLaneResult, error) {
	family := strings.TrimSpace(opts.TaskFamily)
	if family == "" {
		return ProbeLaneResult{}, errors.New("probe: TaskFamily is required")
	}

	mode := opts.Mode
	if strings.TrimSpace(mode.Name) == "" {
		mode = modepolicy.BuildMode()
	}

	route := normalizeTurnRoutePlan(turnRoutePlan{
		Family: family,
		Reason: "explicit_task_family",
	}, mode)
	brief := turnRoutePlanRuntimeBrief(route, mode, append([]string(nil), opts.AllowedTools...))

	return ProbeLaneResult{
		InitialLane:   route.InitialLane,
		FollowupLane:  route.FollowupLane,
		Reason:        route.Reason,
		Contract:      route.Contract,
		BriefTaskFam:  brief.TaskFamily,
		AllowedToolsN: len(brief.AllowedTools),
	}, nil
}

// FormatProbeResult is a small helper the CLI uses to print one
// probe's outcome on a single line. Centralized here so the format
// stays consistent across `probe lane` invocations and any future
// consumers of the probe API.
func FormatProbeResult(label string, r ProbeLaneResult) string {
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
