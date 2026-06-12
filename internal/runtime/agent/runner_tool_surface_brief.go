package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
	"strings"
)

func (r *Runner) visibleBriefForToolSurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	visible := runnerVisibleBriefForToolSurface(brief, results)
	if r == nil || r.Workspace == nil ||
		(!implementationPreWriteToolSurfaceActive(brief, results) &&
			!implementationTargetSourcePending(brief, results) &&
			!implementationTargetWriteReadySurfaceActive(brief, results) &&
			!implementationScopedEditSurfaceActive(brief, results)) {

		return visible
	}

	allowed := implementationPreWriteAllowedTools(r.advertisedAllowedTools(r.Workspace.AllowedTools()))
	if implementationTargetSourcePending(brief, results) {
		allowed = implementationTargetSourceAllowedTools(r.advertisedAllowedTools(r.Workspace.AllowedTools()))
	} else if implementationTargetWriteReadySurfaceActive(brief, results) {
		allowed = implementationTargetWriteReadyAllowedToolsForBrief(r.advertisedAllowedTools(r.Workspace.AllowedTools()), brief, results)
	} else if implementationScopedEditSurfaceActive(brief, results) {
		allowed = implementationScopedEditAllowedToolsWithRegistry(r.advertisedAllowedTools(r.Workspace.AllowedTools()), r.Workspace.ExternalToolsRegistry())
	}

	visible.AllowedTools = allowed
	visible.ToolManifest = nil

	if len(allowed) > 0 {
		visible.ToolManifest = runnerSanitizedToolManifest(allowed)
	}

	return workerruntime.NormalizeRuntimeBrief(visible)
}

func (r *Runner) textualToolProtocolPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	if r == nil || r.Client == nil || r.Client.SupportsNativeTools() {
		return ""
	}

	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if len(brief.ToolManifest) == 0 {
		return ""
	}

	entries := textualToolProtocolEntries(brief)

	var b strings.Builder

	b.WriteString("# Textual Tool Call Protocol\n\n")
	b.WriteString("Native tool calls are unavailable for this backend. Idiom will execute tools only when your entire reply is one textual JSON tool request.\n\n")
	b.WriteString("When taking a tool action, reply with exactly one JSON object for a real allowed tool call and no prose, markdown commentary, XML tags, or second JSON object. Do not copy generic field names or schema examples as values.\n\n")
	b.WriteString("Keep structured fields structured: arrays and objects must be JSON arrays/objects, not quoted JSON strings. For `submit_plan`, `edits` must be an array and every edit object must include `path`.\n")
	b.WriteString("Use the runtime brief's `tool_manifest` for allowed tools and fields. If the runtime brief requires a tool, do not answer in prose while that tool contract is still open.\n")

	if planThenExecutePlannerBrief(brief) && toolManifestHasTool(brief.ToolManifest, workspace.ToolNameSubmitPlan) {
		b.WriteString("\nFor plan-execute `submit_plan`, emit one JSON object with top-level `tool`, `rationale`, and `edits`. Use only real paths and complete current/final source text from gathered evidence; never copy placeholders, ellipses, `TODO`, schema examples, or `existing code` comments.\n")
	}

	if len(entries) > 0 {
		b.WriteString("\nCurrent required/allowed examples:\n")

		for _, entry := range entries {
			b.WriteString("- ")

			tool := strings.TrimSpace(entry.Tool)
			b.WriteString(tool)

			if req := strings.TrimSpace(entry.Request); req != "" {
				b.WriteByte(' ')
				b.WriteString(req)
			}

			if purpose := strings.TrimSpace(entry.Purpose); purpose != "" {
				b.WriteString(" (")
				b.WriteString(purpose)
				b.WriteString(")")
			}

			b.WriteByte('\n')
		}
	}

	return strings.TrimSpace(b.String())
}

func toolManifestHasTool(manifest []workerruntime.ToolManifestEntry, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	for _, entry := range manifest {
		if strings.TrimSpace(entry.Tool) == name {
			return true
		}
	}

	return false
}

func runnerSanitizedToolManifest(tools []string) []workerruntime.ToolManifestEntry {
	return workerruntime.BuildToolManifest(tools)
}

func textualToolProtocolEntries(brief workerruntime.WorkerRuntimeBrief) []workerruntime.ToolManifestEntry {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if len(brief.ToolManifest) == 0 {
		return nil
	}

	suppressWholeFileWrites := scopedImplementationUsesBoundedSourceEdits(brief, brief.RequiredEditPaths)
	required := make(map[string]struct{}, len(brief.RequiredTools))

	for _, tool := range brief.RequiredTools {
		for _, concrete := range textualToolProtocolConcreteRequiredTools(tool) {
			if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(concrete) {
				continue
			}

			if concrete != "" {
				required[concrete] = struct{}{}
			}
		}
	}

	selected := make([]workerruntime.ToolManifestEntry, 0, len(brief.ToolManifest))
	seenEntries := make(map[string]struct{}, len(brief.ToolManifest))
	seenTools := make(map[string]struct{}, len(brief.ToolManifest))

	if len(required) > 0 {
		for _, entry := range brief.ToolManifest {
			tool := strings.TrimSpace(entry.Tool)
			if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(tool) {
				continue
			}

			if _, ok := required[tool]; ok {
				key := textualToolProtocolEntryKey(entry)
				if _, seen := seenEntries[key]; seen {
					continue
				}

				selected = append(selected, entry)
				seenEntries[key] = struct{}{}
				seenTools[tool] = struct{}{}
			}
		}
	}

	const maxExamples = 8

	for _, entry := range brief.ToolManifest {
		if len(selected) >= maxExamples {
			break
		}

		tool := strings.TrimSpace(entry.Tool)
		if tool == "" {
			continue
		}

		if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(tool) {
			continue
		}

		if _, ok := seenTools[tool]; ok {
			continue
		}

		key := textualToolProtocolEntryKey(entry)
		if _, seen := seenEntries[key]; seen {
			continue
		}

		selected = append(selected, entry)
		seenEntries[key] = struct{}{}
		seenTools[tool] = struct{}{}
	}

	return selected
}

func textualToolProtocolWholeFileWriteTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameWriteFile, workspace.ToolNameAppendFile:
		return true
	default:
		return false
	}
}

func textualToolProtocolConcreteRequiredTools(tool string) []string {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "edit_file_or_apply_patch":
		return []string{workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameApplyPatch}
	case "source_sanity_or_verification_shell", "verification_shell":
		return []string{workspace.ToolNameSourceSanity, workspace.ToolNameShell}
	case "bounded_workspace_evidence_tool":
		return []string{workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameSearchContent, workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery}
	default:
		if tool == "" {
			return nil
		}

		return []string{tool}
	}
}

func textualToolProtocolEntryKey(entry workerruntime.ToolManifestEntry) string {
	return strings.TrimSpace(entry.Tool) + "\x00" + strings.TrimSpace(entry.Request)
}
