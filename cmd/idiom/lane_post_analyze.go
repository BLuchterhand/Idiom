package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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

// editToolNames are the write-class tools whose presence in the
// emitted tool-call stream means the lane DID attempt the
// implementation contract. Mirrors the grep alternation in
// run_idiom_impl.sh exactly so behavior parity is provable.
var editToolNames = []string{
	"edit_file",
	"write_file",
	"append_file",
	"apply_patch",
	"submit_plan",
}

// analyzeLaneOutput scans captured stdout for the three failure
// modes Phase 6.5 lifted from Colony's wrapper:
//
//  1. Read-only lane: write-capable brief routed to a read-only
//     contract (an idiom routing regression).
//  2. No edit attempt: run completed without any write-class tool
//     call AND without a `<<QUESTION>>:` blocker AND without a
//     typed `completion_status: blocked` event, and without a typed
//     plan-execute source-satisfied closeout (an idiom completion-
//     contract regression).
//  3. Non-zero subcommand exit: handled by the caller; we only
//     surface (1) and (2) here.
//
// Returns one degradation per detected class. An empty slice means
// the run looks clean.
func analyzeLaneOutput(out []byte, subcommandExit int) []laneDegradation {
	var degraded []laneDegradation

	if marker, ok := containsAnyReadOnlyMarker(out); ok {
		degraded = append(degraded, laneDegradation{
			kind:    "read_only_lane",
			details: "saw " + marker + " in subcommand output (write-capable brief routed to a read-only contract)",
		})
	}

	// (2) only fires when the subcommand exited cleanly. A non-zero
	// exit already signals failure; piling a "no-edit" degradation
	// on top would be noise. The wrapper's exit code still surfaces
	// the underlying failure regardless.
	if subcommandExit == 0 && !sawEditAttempt(out) {
		degraded = append(degraded, laneDegradation{
			kind:    "no_edit_attempt",
			details: "no edit_file/write_file/append_file/apply_patch/submit_plan call AND no <<QUESTION>>: blocker AND no typed completion_status=blocked/source_satisfied event",
		})
	}

	return degraded
}

// containsAnyReadOnlyMarker is a substring search across the
// captured output. Returns the first marker that hit so the
// degradation message can name it.
func containsAnyReadOnlyMarker(out []byte) (string, bool) {
	for _, marker := range readOnlyMarkers {
		if bytes.Contains(out, []byte(marker)) {
			return marker, true
		}
	}
	return "", false
}

// sawEditAttempt returns true when the captured output proves the
// lane reached an edit-capable conclusion: either it called a
// write-class tool, emitted a `<<QUESTION>>:` blocker, terminated
// with a typed `completion_status: blocked` event, or emitted a typed
// plan-execute source-satisfied event.
//
// "Edit attempt" is broader than "edit succeeded" — a tool call
// that the runner rejected still counts because it proves the lane
// tried to write rather than silently exiting clean.
func sawEditAttempt(out []byte) bool {
	if bytes.Contains(out, []byte("<<QUESTION>>:")) {
		return true
	}
	if hasBlockedCompletionEvent(out) {
		return true
	}
	if hasSourceSatisfiedEvent(out) {
		return true
	}
	for _, tool := range editToolNames {
		// Match the JSON shape `"tool":"<name>"` to avoid false
		// positives on prose mentions. The actual emission point
		// is `internal/agent` writing tool-call telemetry, which
		// uses this exact field name.
		needle := fmt.Sprintf(`"tool":"%s"`, tool)
		if bytes.Contains(out, []byte(needle)) {
			return true
		}
	}
	return false
}

// hasSourceSatisfiedEvent walks the JSONL stream looking for the
// typed plan-execute event emitted after the planner's
// `Decision: source-satisfied` answer has been checked against every
// required edit path. This is a legitimate terminal outcome, not a
// no-op implementation run.
func hasSourceSatisfiedEvent(out []byte) bool {
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		eventType := jsonString(ev["type"])
		if eventType == "idiom.plan_execute.source_satisfied" || eventType == "source_satisfied" {
			return true
		}
	}
	return false
}

// hasBlockedCompletionEvent walks the JSONL stream looking for a
// `completion_status` event whose `status` is `blocked`. This is
// the typed-blocker shape idiom emits when the runner stops the
// lane on a structural condition (read-budget exhausted, write-
// guard rejected, etc.) — counts as "the lane reached a defined
// stopping point," not "silently ran without trying anything."
func hasBlockedCompletionEvent(out []byte) bool {
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev["type"] != "completion_status" {
			continue
		}
		status, _ := ev["status"].(string)
		if strings.EqualFold(status, "blocked") {
			return true
		}
	}
	return false
}

// laneSessionSummary holds the token-usage fields the lane wrapper
// extracts from a `session_summary` JSON event emitted by the
// subcommand at Close(). Only populated when the subcommand was
// invoked with --json (the wrapper forwards user-supplied --json
// today; #87's token tracking is opt-in by that flag).
type laneSessionSummary struct {
	SessionID        string
	ModelCalls       int
	ToolCalls        int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	PeakPromptTokens int
	ContextSize      int
	DurationMS       int
	StopReason       string
}

// parseSessionSummary scans the JSONL output stream for the last
// `session_summary` event and returns the token totals. Returns
// ok=false when no such event is present (subcommand ran without
// --json, or exited before Close()). The "last" choice is
// deliberate: if a subcommand re-emits during error recovery, the
// final summary is authoritative.
func parseSessionSummary(out []byte) (laneSessionSummary, bool) {
	var found bool
	var result laneSessionSummary
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev["type"] != "session_summary" {
			continue
		}
		result = laneSessionSummary{
			SessionID:        jsonString(ev["session_id"]),
			ModelCalls:       jsonInt(ev["model_calls"]),
			ToolCalls:        jsonInt(ev["tool_calls"]),
			PromptTokens:     jsonInt(ev["prompt_tokens"]),
			CompletionTokens: jsonInt(ev["completion_tokens"]),
			TotalTokens:      jsonInt(ev["total_tokens"]),
			PeakPromptTokens: jsonInt(ev["peak_prompt_tokens"]),
			ContextSize:      jsonInt(ev["ctx_size"]),
			DurationMS:       jsonInt(ev["duration_ms"]),
			StopReason:       jsonString(ev["stop_reason"]),
		}
		found = true
	}
	return result, found
}

func parseLaneQueuePool(out []byte) string {
	pool := ""
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		eventType := jsonString(ev["type"])
		if eventType != "idiom.plan_execute.queue_acquired" && eventType != "idiom.implement.queue_acquired" {
			continue
		}
		if candidate := jsonString(ev["pool"]); candidate != "" {
			pool = candidate
		}
	}
	return pool
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
