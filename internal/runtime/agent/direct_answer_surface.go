// Package agent: DirectAnswerSurface defines the typed inventory for the
// lightweight direct-answer surface. It is the single authority for which
// tools, capabilities, and terminal states are visible in direct-answer mode.
//
// Other atoms (A1 exact-path prefetch, A2 citation classifier) consume this
// surface through DirectAnswerSurfaceForBrief and
// DirectAnswerSurfacePrefetchEligibility before engaging direct-answer-specific
// behavior. A1 must not run against an unknown, stale, or missing surface.
//
// Design doc: docs/design-direct-answer-efficiency.md, atom A6.
package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

// DirectAnswerSurfaceSchemaVersion identifies the schema for the surface
// inventory JSON payload in telemetry events.
const DirectAnswerSurfaceSchemaVersion = "idiom.direct_answer_surface.v1"

// DirectAnswerSurfaceVersion identifies the current surface inventory version.
// Bump when tools, capabilities, or terminal states change. A1 compares this
// against its expected minimum version before engaging prefetch.
const DirectAnswerSurfaceVersion = "2026-06-11"

// DirectAnswerSurfaceCapReadOnlyInvestigation indicates the surface supports
// non-trivial read-only investigation (search followed by bounded source read).
const DirectAnswerSurfaceCapReadOnlyInvestigation = "read_only_investigation"

// DirectAnswerSurfaceCapSourceBodyPrefetch is the future capability that A1
// exact-path prefetch checks before engaging. Not yet advertised — A6
// surface must advertise it before A1 can activate.
const DirectAnswerSurfaceCapSourceBodyPrefetch = "source_body_prefetch"

// DirectAnswerSurfaceTerminalUncertaintyProse is the terminal state for
// normal uncertainty prose when evidence is insufficient.
const DirectAnswerSurfaceTerminalUncertaintyProse = "uncertainty_prose"

// DirectAnswerSurfaceTerminalAnswerableEvidence is the terminal state for
// evidence-sufficient answers.
const DirectAnswerSurfaceTerminalAnswerableEvidence = "answerable_evidence"

// DirectAnswerSurface is the typed inventory for the lightweight direct-answer
// surface. It defines exactly what the model-facing protocol projects in a
// direct-answer lane.
type DirectAnswerSurface struct {
	// SchemaVersion identifies the JSON schema for this payload.
	SchemaVersion string `json:"schema_version"`
	// Version is the surface inventory version string.
	Version string `json:"version"`
	// Capabilities lists the direct-answer capabilities this surface
	// advertises. Consumers (A1, telemetry) check this before engaging.
	Capabilities []string `json:"capabilities"`
	// ToolNames is the canonical set of tools visible on this surface.
	ToolNames []string `json:"tool_names"`
	// TerminalStates lists valid terminal outcomes for this surface.
	TerminalStates []string `json:"terminal_states"`
}

// DirectAnswerSurfaceForBrief returns the surface inventory when the brief
// identifies a direct-answer lane. Returns (inventory, true) for valid
// direct-answer lanes, (DirectAnswerSurface{}, false) otherwise.
//
// The returned surface includes only read-only investigation tools and
// excludes write/mutation tools, submit_plan, and plan-execute executor
// surfaces. Non-trivial read-only investigation (search + bounded source read)
// remains available.
func DirectAnswerSurfaceForBrief(brief workerruntime.WorkerRuntimeBrief) (DirectAnswerSurface, bool) {
	if !readOnlyDirectAnswerLane(brief) {
		return DirectAnswerSurface{}, false
	}
	return DirectAnswerSurface{
		SchemaVersion: DirectAnswerSurfaceSchemaVersion,
		Version:       DirectAnswerSurfaceVersion,
		Capabilities:  directAnswerSurfaceCapabilities(),
		ToolNames:     directAnswerSurfaceToolNames(brief),
		TerminalStates: []string{
			DirectAnswerSurfaceTerminalAnswerableEvidence,
			DirectAnswerSurfaceTerminalUncertaintyProse,
		},
	}, true
}

// DirectAnswerSurfacePrefetchEligibility checks whether this surface supports
// source-body prefetch. A1 implementations must call this before attempting
// exact-path prefetch. Returns (eligible, missReason) where missReason is
// non-empty when not eligible.
//
// Eligibility requires:
//  1. A non-zero surface (not the zero value).
//  2. The surface version is recognised (matches DirectAnswerSurfaceVersion).
//  3. The surface advertises source_body_prefetch capability.
//
// Normal model-led investigation remains available regardless.
func DirectAnswerSurfacePrefetchEligibility(surface DirectAnswerSurface) (eligible bool, missReason string) {
	if surface.SchemaVersion == "" || surface.Version == "" {
		return false, "direct_answer_surface_missing"
	}
	if surface.Version != DirectAnswerSurfaceVersion {
		return false, "direct_answer_surface_stale: version=" + surface.Version + " expected=" + DirectAnswerSurfaceVersion
	}
	if !surfaceSupportsCapability(surface, DirectAnswerSurfaceCapSourceBodyPrefetch) {
		return false, "direct_answer_surface_no_source_body_prefetch_capability"
	}
	return true, ""
}

// DirectAnswerSurfaceSupportsReadOnlyInvestigation reports whether the surface
// advertises the read_only_investigation capability, meaning non-trivial
// investigation (search + bounded source read) remains available.
func DirectAnswerSurfaceSupportsReadOnlyInvestigation(surface DirectAnswerSurface) bool {
	return surfaceSupportsCapability(surface, DirectAnswerSurfaceCapReadOnlyInvestigation)
}

// surfaceSupportsCapability reports whether the surface advertises the given capability.
func surfaceSupportsCapability(surface DirectAnswerSurface, cap string) bool {
	for _, c := range surface.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// directAnswerSurfaceCapabilities returns the capabilities the current
// direct-answer surface advertises. This is the single source of truth;
// add new capabilities here as they are implemented.
func directAnswerSurfaceCapabilities() []string {
	return []string{
		DirectAnswerSurfaceCapReadOnlyInvestigation,
		// source_body_prefetch is not yet advertised — A1 must remain
		// disabled until it is added here and the version is bumped.
	}
}

// directAnswerSurfaceToolNames returns the canonical tool list for the
// direct-answer surface: only read-only investigation tools, no write/mutation
// tools, no submit_plan, no plan-execute executor protocol.
func directAnswerSurfaceToolNames(_ workerruntime.WorkerRuntimeBrief) []string {
	return defaultDirectAnswerSurfaceTools()
}

// defaultDirectAnswerSurfaceTools returns the base set of tools that are
// always available on the direct-answer surface. These are read-only
// investigation tools that let the model search, read, and navigate the
// workspace without mutation capabilities.
//
// Explicitly excluded:
//   - write_file, edit_file, append_file, apply_patch, go_move_declarations,
//     delegate_edit, mkdir (write tools)
//   - submit_plan (plan-execute surface)
//   - note (scratch metadata — direct-answer answers don't persist findings)
//   - task_ledger (planning and tracking)
//   - shell (command execution)
//
// Tools kept:
//   - read_file, search_files, search_content — exact file/path investigation
//   - code_navigate — language-aware navigation
//   - context_pack, context_query, context_zoom — context graph discovery
//   - discovery_query — composite discovery
//   - structural_search — AST-pattern search
//   - file_stats — directory/file statistics
//   - workspace language tools (go_*, cpp_*, python_*, ts_*, rust_*, java_*)
//   - git history tools (history_range, history_symbol, blame_range)
//   - harness_audit, read_only_capability — harness self-inspection
//   - mcp:* — external read-only tools
func defaultDirectAnswerSurfaceTools() []string {
	var tools []string

	// Source inspection: exact file reading, search, navigation
	tools = append(tools, "read_file", "search_files", "search_content",
		"read_only_capability", "code_navigate")

	// Context graph / discovery
	tools = append(tools, "context_pack", "context_query", "context_zoom",
		"discovery_query", "structural_search", "file_stats")

	// Language-aware navigation via code_navigate capabilities,
	// plus Go-specific structured tools
	tools = append(tools, workspace.RuntimeSourceInspectionToolNames()...)

	// Context discovery tools (context_pack, context_query, etc.)
	tools = append(tools, workspace.RuntimeContextDiscoveryToolNames()...)

	// Git history inspection
	tools = append(tools, workspace.RuntimeHistoryInspectionToolNames()...)

	// Harness self-inspection
	tools = append(tools, "harness_audit")

	// Read-only task delegation is preserved for direct-answer lanes that
	// return child packets; existing runtime contracts depend on it.
	tools = append(tools, "task")

	// Filter out explicit exclusions
	excluded := directAnswerSurfaceExcludedTools()
	filtered := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ex := excluded[tool]; ex {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		filtered = append(filtered, tool)
	}
	return filtered
}

// directAnswerSurfaceExcludedTools returns the set of tools that must never
// appear on the direct-answer surface.
func directAnswerSurfaceExcludedTools() map[string]struct{} {
	excluded := make(map[string]struct{})
	for _, name := range workspace.WorkspaceWriteToolNames() {
		excluded[name] = struct{}{}
	}
	excluded["submit_plan"] = struct{}{}
	excluded["note"] = struct{}{}
	excluded["task_ledger"] = struct{}{}
	excluded["shell"] = struct{}{}
	return excluded
}

// EmitDirectAnswerSurfaceEvent creates and emits a telemetry event with the
// surface inventory metadata. Called once per session when the surface is
// first activated. The runner passes emitEvent because it's the same package.
func EmitDirectAnswerSurfaceEvent(emitFn func(event RunEvent), surface DirectAnswerSurface) {
	if emitFn == nil {
		return
	}
	emitFn(marshalRunEvent("direct_answer_surface", map[string]any{
		"schema_version":  surface.SchemaVersion,
		"version":         surface.Version,
		"capabilities":    surface.Capabilities,
		"tool_count":      len(surface.ToolNames),
		"terminal_states": surface.TerminalStates,
	}))
}

// DirectAnswerSurfaceForBriefTools is a convenience wrapper that returns the
// tool names from DirectAnswerSurfaceForBrief as a flat string slice or nil
// when the brief is not a direct-answer lane.
func DirectAnswerSurfaceForBriefTools(brief workerruntime.WorkerRuntimeBrief) []string {
	surface, ok := DirectAnswerSurfaceForBrief(brief)
	if !ok {
		return nil
	}
	return surface.ToolNames
}
