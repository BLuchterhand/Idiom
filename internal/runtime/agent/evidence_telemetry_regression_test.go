package agent

import (
	"encoding/json"
	"slices"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestFailedReadFileMissingPathIsNotAcceptedEvidence(t *testing.T) {
	const (
		missingPath   = "internal/runtime/agent/missing.go"
		inspectedPath = "internal/runtime/agent/runner.go"
	)

	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/missing.go lines 1-80",
			Error:   "internal/runtime/agent/missing.go was not found",
		},
		{
			Tool:    "search_content",
			Summary: "No matches in internal/runtime/agent/zero_hit.go",
			Output:  "0 matching line(s)",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner.go lines 1-8",
			Output:  "package agent\n\nfunc evidenceTelemetryRegressionFixture() {}\n",
		},
	}

	for name, paths := range map[string][]string{
		"direct probes":           directProbePathsFromToolResults(results, 8),
		"mentioned evidence":      mentionedEvidencePathsFromToolResults(results, 8),
		"implementation evidence": acceptedImplementationEvidencePaths(results),
	} {
		if slices.Contains(paths, missingPath) {
			t.Fatalf("%s promoted missing read_file path as accepted evidence: %#v", name, paths)
		}

		if slices.Contains(paths, "internal/runtime/agent/zero_hit.go") {
			t.Fatalf("%s promoted zero-hit search path as accepted evidence: %#v", name, paths)
		}

		if !slices.Contains(paths, inspectedPath) {
			t.Fatalf("%s should preserve successful read_file evidence path %q, got %#v", name, inspectedPath, paths)
		}
	}
}

func TestModelCallTelemetryDistinguishesParsedTextualTool(t *testing.T) {
	var event RunEvent

	runner := &Runner{
		OnEvent: func(got RunEvent) {
			event = got
		},
	}

	runner.emitModelCallTraceWithOptions(
		1,
		"regression",
		[]llama.ChatMessage{{Role: "user", Content: "inspect runner"}},
		llama.ChatMessage{
			Role:    "assistant",
			Content: `{"tool":"read_file","path":"internal/runtime/agent/runner.go"}`,
		},
		7,
		llama.ChatOptions{},
		false,
		nil,
	)

	if event.Role != "model_call" {
		t.Fatalf("expected model_call event, got %#v", event)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		t.Fatalf("decode model_call payload: %v", err)
	}

	if payload["chosen_tool"] != "<answer>" {
		t.Fatalf("textual tool request must not masquerade as native chosen_tool, got %#v", payload)
	}

	if payload["parsed_textual_tool"] != "read_file" {
		t.Fatalf("model_call telemetry missing parsed textual tool identity: %#v", payload)
	}

	if payload["parsed_tool_transport"] != "textual" {
		t.Fatalf("model_call telemetry missing textual transport: %#v", payload)
	}

	if payload["parsed_tool_request_valid"] != true {
		t.Fatalf("model_call telemetry should mark parsed textual request valid: %#v", payload)
	}
}

func TestModelCallTelemetryReportsParsedKeyValueWriteWithoutRawEditText(t *testing.T) {
	var event RunEvent

	runner := &Runner{
		OnEvent: func(got RunEvent) {
			event = got
		},
	}

	runner.emitModelCallTraceWithOptions(
		1,
		"regression",
		[]llama.ChatMessage{{Role: "user", Content: "edit the label"}},
		llama.ChatMessage{
			Role:    "assistant",
			Content: "tool: edit_file\npath: internal/labels/labels.go\nold_text: return \"Save changes\"\nnew_text: return \"Save\"\ndescription: update label",
		},
		7,
		llama.ChatOptions{},
		false,
		nil,
	)

	if event.Role != "model_call" {
		t.Fatalf("expected model_call event, got %#v", event)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		t.Fatalf("decode model_call payload: %v", err)
	}

	if payload["chosen_tool"] != "<answer>" {
		t.Fatalf("textual write request must not masquerade as native chosen_tool, got %#v", payload)
	}

	if payload["parsed_textual_tool"] != "edit_file" || payload["parsed_tool_transport"] != toolRequestNormalizerTransportKeyValue || payload["parsed_tool_request_valid"] != true {
		t.Fatalf("model_call telemetry should report parsed key-value write shape without treating it as native: %#v", payload)
	}

	for _, forbidden := range []string{"executed_textual_tool", "textual_tool_transport", "old_text", "new_text"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("textual write telemetry must not emit raw edit field %q: %#v", forbidden, payload)
		}
	}
}

func TestToolRequestNormalizerTelemetryExposesParsedTextualTool(t *testing.T) {
	var event RunEvent

	runner := &Runner{
		OnEvent: func(got RunEvent) {
			event = got
		},
	}

	runner.emitToolRequestNormalizerEvent(
		toolRequestNormalizerOutcomeNormalized,
		toolRequestNormalizerTransportTextual,
		workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"},
		nil,
		"",
	)

	if event.Role != "tool_request_normalizer" {
		t.Fatalf("expected tool_request_normalizer event, got %#v", event)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		t.Fatalf("decode tool_request_normalizer payload: %v", err)
	}

	if payload["tool"] != "read_file" {
		t.Fatalf("existing tool field should remain compatible, got %#v", payload)
	}

	if payload["parsed_textual_tool"] != "read_file" {
		t.Fatalf("normalizer telemetry missing parsed textual tool identity: %#v", payload)
	}

	if _, ok := payload["executed_textual_tool"]; ok {
		t.Fatalf("normalizer telemetry is pre-dispatch and must not claim execution: %#v", payload)
	}

	if payload["textual_tool_transport"] != "textual" {
		t.Fatalf("normalizer telemetry missing textual transport: %#v", payload)
	}
}

func TestToolRequestNormalizerTelemetryDoesNotTreatDeterministicRecoveryAsTextualTool(t *testing.T) {
	var event RunEvent

	runner := &Runner{
		OnEvent: func(got RunEvent) {
			event = got
		},
	}

	runner.emitToolRequestNormalizerEvent(
		toolRequestNormalizerOutcomeRequiresTerminal,
		"deterministic_required_source_recovery",
		workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"},
		nil,
		"",
	)

	if event.Role != "tool_request_normalizer" {
		t.Fatalf("expected tool_request_normalizer event, got %#v", event)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		t.Fatalf("decode tool_request_normalizer payload: %v", err)
	}

	if payload["tool"] != "read_file" {
		t.Fatalf("existing tool field should remain compatible, got %#v", payload)
	}

	for _, forbidden := range []string{"parsed_textual_tool", "executed_textual_tool", "textual_tool_transport"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("deterministic recovery must not be labeled as model textual tool telemetry via %q: %#v", forbidden, payload)
		}
	}
}

func TestBlockedReadOnlyDirectAnswerTelemetryKeepsAcceptedEvidenceEmpty(t *testing.T) {
	var events []RunEvent

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain telemetry accounting.",
		},
		OnEvent: func(got RunEvent) {
			events = append(events, got)
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 1-20",
		Output:  "package agent\n\nfunc telemetryAccounting() {}\n",
	}}
	acceptance := finalAnswerAcceptance{
		SchemaVersion: finalAnswerAcceptanceSchema,
		Decision:      finalAnswerAcceptanceRejected,
		BlockerKind:   "answer_objective_unmet",
		Reason:        "answer did not address the objective",
		EvidencePaths: nil,
	}

	runner.emitFinalAnswerTelemetry("This answer is blocked.", results, acceptance)

	if len(events) != 3 {
		t.Fatalf("expected three blocked telemetry events, got %#v", events)
	}

	for _, event := range events {
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
			t.Fatalf("decode %s payload: %v", event.Role, err)
		}

		if pathsLen(payload["evidence_paths"]) != 0 {
			t.Fatalf("%s must keep rejected accepted evidence empty, got %#v", event.Role, payload)
		}

		if got := stringSliceFromJSON(payload["presentation_evidence_paths"]); !slices.Contains(got, "internal/runtime/agent/runner.go") {
			t.Fatalf("%s should expose presentation evidence separately, got %#v", event.Role, payload)
		}
	}
}

func TestAcceptedReadOnlyDirectAnswerTelemetryKeepsAcceptedEvidenceSeparateFromPresentation(t *testing.T) {
	var events []RunEvent

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain telemetry accounting.",
		},
		OnEvent: func(got RunEvent) {
			events = append(events, got)
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 1-20",
		Output:  "package agent\n\nfunc telemetryAccounting() {}\n",
	}}
	acceptance := finalAnswerAcceptance{
		SchemaVersion: finalAnswerAcceptanceSchema,
		Decision:      finalAnswerAcceptanceAccepted,
		EvidencePaths: nil,
	}

	runner.emitFinalAnswerTelemetry("Telemetry accounting is implemented in internal/runtime/agent/runner.go.", results, acceptance)

	if len(events) != 3 {
		t.Fatalf("expected three accepted telemetry events, got %#v", events)
	}

	for _, event := range events {
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
			t.Fatalf("decode %s payload: %v", event.Role, err)
		}

		if pathsLen(payload["evidence_paths"]) != 0 {
			t.Fatalf("%s must keep accepted evidence empty when acceptance has none, got %#v", event.Role, payload)
		}

		if got := stringSliceFromJSON(payload["presentation_evidence_paths"]); !slices.Contains(got, "internal/runtime/agent/runner.go") {
			t.Fatalf("%s should expose presentation evidence separately, got %#v", event.Role, payload)
		}
	}
}

func TestAcceptedFinalAnswerTelemetryPreservesAcceptedEvidencePaths(t *testing.T) {
	var events []RunEvent

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain accepted evidence accounting.",
		},
		OnEvent: func(got RunEvent) {
			events = append(events, got)
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/presentation_only.go lines 1-20",
		Output:  "package agent\n\nfunc presentationOnly() {}\n",
	}}
	acceptance := finalAnswerAcceptance{
		SchemaVersion: finalAnswerAcceptanceSchema,
		Decision:      finalAnswerAcceptanceAccepted,
		EvidencePaths: []string{"internal/runtime/agent/accepted.go"},
	}

	runner.emitFinalAnswerTelemetry("Accepted evidence was already computed by the gate.", results, acceptance)

	if len(events) != 3 {
		t.Fatalf("expected three accepted telemetry events, got %#v", events)
	}

	for _, event := range events {
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
			t.Fatalf("decode %s payload: %v", event.Role, err)
		}

		got := stringSliceFromJSON(payload["evidence_paths"])
		if !slices.Contains(got, "internal/runtime/agent/accepted.go") || slices.Contains(got, "internal/runtime/agent/presentation_only.go") {
			t.Fatalf("%s should preserve accepted evidence paths without presentation replacement, got %#v", event.Role, payload)
		}
	}
}

func pathsLen(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case []any:
		return len(typed)
	default:
		return 1
	}
}

func stringSliceFromJSON(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}

	return out
}
