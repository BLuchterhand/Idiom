package agent

import (
	"encoding/json"
	"strings"

	"idiom/internal/planintent"
	"idiom/internal/workspace"
)

func (r *Runner) compilePlanIntentToolRequest(text string) (workspace.ToolRequest, string, bool) {
	if !r.planIntentCompilationAllowed() {
		return workspace.ToolRequest{}, "", false
	}
	payload, ok := exactPlanIntentPayload(text)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}
	var artifact planintent.Artifact
	if err := json.Unmarshal([]byte(payload), &artifact); err != nil {
		r.emitPlanIntentStageEvent("edit_intent_validated", map[string]any{
			"schema_version":    planintent.SchemaVersion,
			"validation_result": "rejected",
			"failure_class":     "malformed_json",
		})
		return workspace.ToolRequest{}, "", false
	}
	if artifact.SchemaVersion != planintent.SchemaVersion {
		return workspace.ToolRequest{}, "", false
	}
	contract := planintent.Contract{
		RequiredEditPaths:  r.RuntimeBrief.RequiredEditPaths,
		CandidateEditPaths: r.RuntimeBrief.AllowedEditPaths,
	}
	if err := planintent.Validate(artifact, contract); err != nil {
		r.emitPlanIntentStageEvent("edit_intent_validated", planIntentValidationPayload(artifact, "rejected", err))
		return workspace.ToolRequest{}, "", false
	}
	r.emitPlanIntentStageEvent("edit_intent_validated", planIntentValidationPayload(artifact, "accepted", nil))
	r.emitPlanIntentStageEvent("patch_compile_started", planIntentCompilePayload(artifact, "started", nil, 0))
	root := ""
	if r != nil && r.Workspace != nil {
		root = r.Workspace.Root()
	}
	edits, err := planintent.Compile(artifact, planintent.CompileOptions{
		WorkspaceRoot: root,
		Contract:      contract,
	})
	if err != nil {
		r.emitPlanIntentStageEvent("patch_compile_result", planIntentCompilePayload(artifact, "rejected", err, 0))
		return workspace.ToolRequest{}, "", false
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: planIntentRationale(artifact),
		Edits:     edits,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		r.emitPlanIntentStageEvent("patch_compile_result", planIntentCompilePayload(artifact, "rejected", err, 0))
		return workspace.ToolRequest{}, "", false
	}
	r.emitPlanIntentStageEvent("patch_compile_result", planIntentCompilePayload(artifact, "accepted", nil, len(edits)))
	return req, string(raw), true
}

func exactPlanIntentPayload(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, "{") {
		return trimmed, strings.Contains(trimmed, `"schema_version"`) && strings.Contains(trimmed, planintent.SchemaVersion)
	}
	if payload, ok, unsafe := exactFencedJSONPayload(trimmed); ok && !unsafe {
		return payload, strings.Contains(payload, `"schema_version"`) && strings.Contains(payload, planintent.SchemaVersion)
	}
	return "", false
}

func planIntentRationale(artifact planintent.Artifact) string {
	summary := strings.TrimSpace(artifact.TaskSummary)
	if summary == "" {
		return "compiled from non-executable idiom.edit_intent.v1 artifact"
	}
	return "compiled from non-executable idiom.edit_intent.v1 artifact: " + summary
}

func planIntentValidationPayload(artifact planintent.Artifact, result string, err error) map[string]any {
	payload := map[string]any{
		"schema_version":     strings.TrimSpace(artifact.SchemaVersion),
		"source":             string(artifact.Source),
		"validation_result":  result,
		"required_paths":     safeTelemetryPathList(artifact.Scope.RequiredEditPaths),
		"candidate_paths":    safeTelemetryPathList(artifact.Scope.CandidateEditPaths),
		"intent_count":       len(artifact.Intents),
		"blocker_count":      len(artifact.Blockers),
		"evidence_ref_count": len(artifact.Scope.EvidenceRefs),
	}
	if err != nil {
		payload["failure_class"] = planIntentErrorCode(err)
	}
	return payload
}

func planIntentCompilePayload(artifact planintent.Artifact, result string, err error, editCount int) map[string]any {
	payload := map[string]any{
		"schema_version":  strings.TrimSpace(artifact.SchemaVersion),
		"source":          string(artifact.Source),
		"compile_outcome": result,
		"required_paths":  safeTelemetryPathList(artifact.Scope.RequiredEditPaths),
		"candidate_paths": safeTelemetryPathList(artifact.Scope.CandidateEditPaths),
		"intent_count":    len(artifact.Intents),
		"edit_count":      editCount,
	}
	if err != nil {
		payload["failure_class"] = planIntentErrorCode(err)
	}
	return payload
}

func planIntentErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if validationErr, ok := err.(*planintent.ValidationError); ok {
		return validationErr.Code
	}
	if compileErr, ok := err.(*planintent.CompileError); ok {
		return compileErr.Code
	}
	return "unknown"
}

func safeTelemetryPathList(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeRunnerPath(path)
		if path != "" {
			out = append(out, path)
		}
	}
	return out
}

func (r *Runner) emitPlanIntentStageEvent(name string, payload map[string]any) {
	if r == nil {
		return
	}
	r.emitEvent(marshalRunEvent(name, payload))
}

func (r *Runner) planIntentCompilationAllowed() bool {
	if r == nil {
		return false
	}
	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}
	return stringsInSetContains(r.RuntimeBrief.AllowedTools, "submit_plan") || stringsInSetContains(r.RuntimeBrief.RequiredTools, "submit_plan")
}

func stringsInSetContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
