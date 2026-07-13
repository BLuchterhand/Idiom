package lane

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	queuecontract "idiom/internal/contracts/queue"
	recoverycontract "idiom/internal/contracts/recovery"
)

// teeBuffer wraps an io.Writer so the lane wrapper can both stream
// the subcommand's stdout to its own caller AND retain a copy for
// post-analysis. Mirrors stdlib bytes.Buffer + io.MultiWriter
// composition, but as a single type so the call site stays readable.
type teeBuffer struct {
	out io.Writer
	buf bytes.Buffer
}

func newTeeBuffer(out io.Writer) *teeBuffer {
	return &teeBuffer{out: out}
}

// Write satisfies io.Writer. We write to the underlying stream
// first; if THAT errors, the buffer copy still happens so the
// post-analysis sees the same bytes the subcommand attempted to
// emit. The error is surfaced to the caller so streaming bugs
// don't get hidden.
func (t *teeBuffer) Write(p []byte) (int, error) {
	n, err := t.out.Write(p)
	t.buf.Write(p[:n])

	return n, err
}

// Bytes returns the captured copy of everything that was written.
// Used by the analyzer; not safe to mutate.
func (t *teeBuffer) Bytes() []byte { return t.buf.Bytes() }

// laneDegradation describes one detected regression class. The
// `kind` field is a stable enum-shaped string that consumers
// (Colony's harness_feedback, future audit hooks) key on; details
// is a short human-readable summary.
type laneDegradation struct {
	kind    string
	details string
}

type laneOutputFacts struct {
	ReadOnlyMarker           string
	SawImplementationAttempt bool
	SourceSatisfied          bool
	ReviewRepairDegraded     bool
	SessionSummary           laneSessionSummary
	HasSessionSummary        bool
	QueuePool                string
	ImplementationState      *recoverycontract.ImplementationState
}

// readOnlyMarkers are the substrings the Colony script grep'd for.
// They show up when idiom routed a write-capable brief to a
// read-only lane — the regression class plan-execute and implement
// must never produce. Any of these in the output stream is a
// reliable degradation signal.
var readOnlyMarkers = []string{
	"verification_request",
	"verification_only",
	"conversation_only",
}

func analyzeLaneOutputFacts(out []byte) laneOutputFacts {
	var facts laneOutputFacts

	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if facts.ReadOnlyMarker == "" {
			if marker, ok := containsAnyReadOnlyMarker(line); ok {
				facts.ReadOnlyMarker = marker
			}
		}

		if bytes.Contains(line, []byte("<<QUESTION>>:")) {
			facts.SawImplementationAttempt = true
		}

		if !facts.SawImplementationAttempt {
			if tool, ok := rawJSONToolName(line); ok && workspace.IsImplementationAttemptTool(tool) {
				facts.SawImplementationAttempt = true
			}
		}

		if line[0] != '{' {
			continue
		}

		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		facts.applyJSONEvent(ev)
	}

	return facts
}

func (facts *laneOutputFacts) applyJSONEvent(ev map[string]any) {
	if facts == nil {
		return
	}

	if workspace.IsImplementationAttemptTool(jsonString(ev["tool"])) {
		facts.SawImplementationAttempt = true
	}

	eventType := jsonString(ev["type"])

	if eventType == "completion_status" && strings.EqualFold(jsonString(ev["status"]), "blocked") {
		facts.SawImplementationAttempt = true
	}

	if parsedEventType, ok := plancontract.ParsePlanExecuteEventType(eventType); ok {
		switch parsedEventType {
		case plancontract.PlanExecuteEventSourceSatisfied:
			facts.SourceSatisfied = true
			facts.SawImplementationAttempt = true
		case plancontract.PlanExecuteEventReviewRepairDegraded:
			facts.ReviewRepairDegraded = true
		case plancontract.PlanExecuteEventAutoExactPlanSubmitted, plancontract.PlanExecuteEventExecutorEdit:
			facts.SawImplementationAttempt = true
		case plancontract.PlanExecuteEventExecutorComplete:
			if jsonNumberPositive(ev["total"]) || jsonNumberPositive(ev["succeeded"]) || jsonNumberPositive(ev["failed"]) {
				facts.SawImplementationAttempt = true
			}
		}
	}

	switch eventType {
	case "session_summary":
		facts.SessionSummary = laneSessionSummary{
			SessionID:                  jsonString(ev["session_id"]),
			ModelCalls:                 jsonInt(ev["model_calls"]),
			ToolCalls:                  jsonInt(ev["tool_calls"]),
			PromptTokens:               jsonInt(ev["prompt_tokens"]),
			CompletionTokens:           jsonInt(ev["completion_tokens"]),
			TotalTokens:                jsonInt(ev["total_tokens"]),
			PromptCacheHitTokens:       jsonInt(ev["prompt_cache_hit_tokens"]),
			PromptCacheMissTokens:      jsonInt(ev["prompt_cache_miss_tokens"]),
			PromptCacheAccountingKnown: jsonBool(ev["prompt_cache_accounting_known"]),
			ReasoningTokens:            jsonInt(ev["reasoning_tokens"]),
			PeakPromptTokens:           jsonInt(ev["peak_prompt_tokens"]),
			ContextSize:                jsonInt(ev["ctx_size"]),
			DurationMS:                 jsonInt(ev["duration_ms"]),
			StopReason:                 jsonString(ev["stop_reason"]),
		}
		facts.HasSessionSummary = true
	case queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueAcquired),
		queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixImplement, queuecontract.AdmissionEventQueueAcquired):
		if candidate := jsonString(ev["pool"]); candidate != "" {
			facts.QueuePool = candidate
		}
	case "completion_status", "implementation_lifecycle_state":
		if state := implementationStateFromEvent(ev); recoverycontract.ImplementationStatePresent(state) {
			facts.ImplementationState = &state
		}
	}
}

// degradations reports the Phase 6.5 failure modes lifted from Colony's
// wrapper. An empty slice means the run looks clean.
func (facts laneOutputFacts) degradations(subcommandExit int) []laneDegradation {
	var degraded []laneDegradation

	if marker := strings.TrimSpace(facts.ReadOnlyMarker); marker != "" {
		degraded = append(degraded, laneDegradation{
			kind:    "read_only_lane",
			details: "saw " + marker + " in subcommand output (write-capable brief routed to a read-only contract)",
		})
	}

	// (2) only fires when the subcommand exited cleanly. A non-zero
	// exit already signals failure; piling a "no-edit" degradation
	// on top would be noise. The wrapper's exit code still surfaces
	// the underlying failure regardless.
	if subcommandExit == 0 && !facts.SawImplementationAttempt {
		degraded = append(degraded, laneDegradation{
			kind:    "no_edit_attempt",
			details: "no implementation-attempt tool call AND no typed plan-execute edit event AND no <<QUESTION>>: blocker AND no typed completion_status=blocked/source_satisfied event",
		})
	}

	return degraded
}

func rawJSONToolName(line []byte) (string, bool) {
	toolField := []byte(`"tool":"`)
	index := bytes.Index(line, toolField)
	if index < 0 {
		return "", false
	}

	remaining := line[index+len(toolField):]
	end := bytes.IndexByte(remaining, '"')
	if end < 0 {
		return "", false
	}

	return string(remaining[:end]), true
}

// containsAnyReadOnlyMarker returns the first read-only marker in value.
func containsAnyReadOnlyMarker(out []byte) (string, bool) {
	for _, marker := range readOnlyMarkers {
		if bytes.Contains(out, []byte(marker)) {
			return marker, true
		}
	}

	return "", false
}

func jsonNumberPositive(v any) bool {
	switch n := v.(type) {
	case float64:
		return n > 0
	case float32:
		return n > 0
	case int:
		return n > 0
	case int64:
		return n > 0
	case json.Number:
		f, err := n.Float64()
		return err == nil && f > 0
	default:
		return false
	}
}

// laneSessionSummary holds the token-usage fields the lane wrapper
// extracts from a `session_summary` JSON event emitted by the
// subcommand at Close(). Only populated when the subcommand was
// invoked with --json (the wrapper forwards user-supplied --json
// today; #87's token tracking is opt-in by that flag).
type laneSessionSummary struct {
	SessionID                  string
	ModelCalls                 int
	ToolCalls                  int
	PromptTokens               int
	CompletionTokens           int
	TotalTokens                int
	PromptCacheHitTokens       int
	PromptCacheMissTokens      int
	PromptCacheAccountingKnown bool
	ReasoningTokens            int
	PeakPromptTokens           int
	ContextSize                int
	DurationMS                 int
	StopReason                 string
}

func implementationStateFromEvent(ev map[string]any) recoverycontract.ImplementationState {
	return recoverycontract.ImplementationState{
		Phase:                      jsonString(ev["phase"]),
		StopReason:                 jsonString(ev["stop_reason"]),
		RemainingRequiredEditPaths: jsonStringSlice(ev["remaining_required_edit_paths"]),
		ChangedPaths:               jsonStringSlice(ev["changed_paths"]),
		SourcedEvidencePaths:       jsonStringSlice(ev["sourced_evidence_paths"]),
		WriteReadyNudgeCount:       jsonInt(ev["write_ready_nudge_count"]),
	}
}

// jsonInt coerces a JSON-decoded value (which arrives as float64
// from encoding/json) into an int. Returns 0 for any other type.
func jsonInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}

	return 0
}

func jsonStringSlice(v any) []string {
	values, ok := v.([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := jsonString(value); text != "" {
			out = append(out, text)
		}
	}

	return out
}
