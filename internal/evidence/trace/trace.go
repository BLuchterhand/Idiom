package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const SchemaVersion = "idiom.run_trace.v1"

type SpanKind string

const (
	SpanRun          SpanKind = "run"
	SpanTurn         SpanKind = "turn"
	SpanModelCall    SpanKind = "model_call"
	SpanToolRequest  SpanKind = "tool_request"
	SpanPolicy       SpanKind = "policy_decision"
	SpanToolResult   SpanKind = "tool_result"
	SpanEvidence     SpanKind = "evidence"
	SpanVerification SpanKind = "verification"
	SpanCompaction   SpanKind = "compaction"
	SpanResume       SpanKind = "resume"
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	StatusObserved  Status = "observed"
)

type Span struct {
	TraceID      string         `json:"trace_id"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	RunID        string         `json:"run_id,omitempty"`
	Kind         SpanKind       `json:"kind"`
	Status       Status         `json:"status"`
	StartedAt    time.Time      `json:"started_at"`
	DurationMS   int64          `json:"duration_ms,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

type Document struct {
	SchemaVersion string `json:"schema_version"`
	TraceID       string `json:"trace_id"`
	RunID         string `json:"run_id,omitempty"`
	Spans         []Span `json:"spans"`
}

type Recorder struct {
	mu            sync.Mutex
	document      Document
	sequence      int
	currentModel  string
	toolSpans     map[string]string
	terminalSpans map[string]bool
}

func NewRecorder(runID string) *Recorder {
	runID = strings.TrimSpace(runID)
	traceID := fingerprint(runID)
	if traceID == "" {
		traceID = fingerprint("idiom-run")
	}
	recorder := &Recorder{
		document:      Document{SchemaVersion: SchemaVersion, TraceID: traceID, RunID: runID},
		toolSpans:     make(map[string]string),
		terminalSpans: make(map[string]bool),
	}
	_, _ = recorder.Record(SpanRun, "", StatusRunning, map[string]any{"run_id": runID})
	return recorder
}

func (r *Recorder) Record(kind SpanKind, parent string, status Status, attributes map[string]any) (string, error) {
	if r == nil {
		return "", fmt.Errorf("trace recorder is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recordLocked(kind, parent, status, attributes)
}

func (r *Recorder) recordLocked(kind SpanKind, parent string, status Status, attributes map[string]any) (string, error) {
	if r.document.SchemaVersion != SchemaVersion {
		return "", fmt.Errorf("unsupported trace schema %q", r.document.SchemaVersion)
	}
	if _, ok := validSpanKinds[kind]; !ok {
		return "", fmt.Errorf("unsupported trace span kind %q", kind)
	}
	if parent != "" && !r.hasSpanLocked(parent) {
		return "", fmt.Errorf("trace parent %q does not exist", parent)
	}
	if parent != "" && r.terminalSpans[parent] {
		return "", fmt.Errorf("trace parent %q is terminal span", parent)
	}
	if status == "" {
		status = StatusObserved
	}
	r.sequence++
	spanID := fmt.Sprintf("%s-%d", r.document.TraceID[:12], r.sequence)
	span := Span{
		TraceID:      r.document.TraceID,
		SpanID:       spanID,
		ParentSpanID: parent,
		RunID:        r.document.RunID,
		Kind:         kind,
		Status:       status,
		StartedAt:    time.Unix(0, int64(r.sequence)).UTC(),
		Attributes:   safeAttributes(attributes),
	}
	r.document.Spans = append(r.document.Spans, span)
	if status == StatusCompleted || status == StatusFailed || status == StatusCanceled {
		r.terminalSpans[spanID] = true
	}
	return spanID, nil
}

func (r *Recorder) hasSpanLocked(id string) bool {
	for _, span := range r.document.Spans {
		if span.SpanID == id {
			return true
		}
	}
	return false
}

func (r *Recorder) RecordRunEvent(role, content string) {
	if r == nil {
		return
	}
	kind, ok := spanKindForRole(role)
	if !ok {
		return
	}
	attrs := map[string]any{
		"event_role":    strings.TrimSpace(role),
		"content_chars": len(content),
		"content_hash":  fingerprint(content),
	}
	r.mu.Lock()
	parent := r.document.Spans[0].SpanID
	status := StatusCompleted
	if kind == SpanModelCall {
		status = StatusRunning
	}
	spanID, err := r.recordLocked(kind, parent, status, attrs)
	if err == nil && kind == SpanModelCall {
		r.currentModel = spanID
	}
	r.mu.Unlock()
}

func (r *Recorder) RecordToolRequest(tool, requestFingerprint string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	parent := r.currentModel
	if parent == "" {
		parent = r.document.Spans[0].SpanID
	}
	spanID, err := r.recordLocked(SpanToolRequest, parent, StatusRunning, map[string]any{
		"tool":                strings.TrimSpace(tool),
		"request_fingerprint": strings.TrimSpace(requestFingerprint),
	})
	if err == nil {
		r.toolSpans[strings.TrimSpace(requestFingerprint)] = spanID
	}
	return spanID
}

func (r *Recorder) RecordToolResult(requestFingerprint string, status Status, attributes map[string]any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	parent := r.toolSpans[strings.TrimSpace(requestFingerprint)]
	if parent == "" {
		parent = r.document.Spans[0].SpanID
	}
	_, _ = r.recordLocked(SpanToolResult, parent, status, attributes)
}

func (r *Recorder) RecordToolPolicy(requestFingerprint, decision string, status Status) {
	r.recordToolChild(SpanPolicy, requestFingerprint, status, map[string]any{"decision": strings.TrimSpace(decision)})
}

func (r *Recorder) RecordToolEvidence(requestFingerprint string, attributes map[string]any) {
	r.recordToolChild(SpanEvidence, requestFingerprint, StatusCompleted, attributes)
}

func (r *Recorder) RecordToolVerification(requestFingerprint, status string) {
	r.recordToolChild(SpanVerification, requestFingerprint, StatusCompleted, map[string]any{"verification_status": strings.TrimSpace(status)})
}

func (r *Recorder) recordToolChild(kind SpanKind, requestFingerprint string, status Status, attributes map[string]any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	parent := r.toolSpans[strings.TrimSpace(requestFingerprint)]
	if parent == "" {
		return
	}
	_, _ = r.recordLocked(kind, parent, status, attributes)
}

func (r *Recorder) Snapshot() Document {
	if r == nil {
		return Document{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	copyDocument := r.document
	copyDocument.Spans = append([]Span(nil), r.document.Spans...)
	return copyDocument
}

type ReplayReport struct {
	Valid      bool     `json:"valid"`
	Violations []string `json:"violations,omitempty"`
}

func Replay(document Document) ReplayReport {
	report := ReplayReport{Valid: true}
	if document.SchemaVersion != SchemaVersion {
		report.Violations = append(report.Violations, "unsupported schema")
	}
	spans := make(map[string]Span, len(document.Spans))
	terminal := make(map[string]bool)
	for _, span := range document.Spans {
		if _, exists := spans[span.SpanID]; exists {
			report.Violations = append(report.Violations, "duplicate span id: "+span.SpanID)
		}
		spans[span.SpanID] = span
		if span.Status == StatusCompleted || span.Status == StatusFailed || span.Status == StatusCanceled {
			terminal[span.SpanID] = true
		}
	}
	for _, span := range document.Spans {
		if span.ParentSpanID != "" {
			if _, exists := spans[span.ParentSpanID]; !exists {
				report.Violations = append(report.Violations, "missing parent: "+span.SpanID)
			} else if terminal[span.ParentSpanID] {
				report.Violations = append(report.Violations, "child of terminal span: "+span.SpanID)
			}
		}
	}
	for _, span := range document.Spans {
		if span.Kind != SpanToolRequest {
			continue
		}
		resultFound := false
		for _, child := range document.Spans {
			if child.ParentSpanID == span.SpanID && child.Kind == SpanToolResult {
				resultFound = true
				break
			}
		}
		if !resultFound {
			report.Violations = append(report.Violations, "tool request has no result: "+span.SpanID)
		}
	}
	if len(document.Spans) == 0 || document.Spans[0].Kind != SpanRun {
		report.Violations = append(report.Violations, "trace has no run root")
	}
	report.Valid = len(report.Violations) == 0
	sort.Strings(report.Violations)
	return report
}

func (r *Recorder) MarshalJSON() ([]byte, error) { return json.Marshal(r.Snapshot()) }

var validSpanKinds = map[SpanKind]struct{}{
	SpanRun: {}, SpanTurn: {}, SpanModelCall: {}, SpanToolRequest: {},
	SpanPolicy: {}, SpanToolResult: {}, SpanEvidence: {}, SpanVerification: {},
	SpanCompaction: {}, SpanResume: {},
}

func spanKindForRole(role string) (SpanKind, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	switch {
	case role == "model_call":
		return SpanModelCall, true
	case strings.Contains(role, "compaction"):
		return SpanCompaction, true
	case strings.Contains(role, "verification") || strings.Contains(role, "sanity"):
		return SpanVerification, true
	case strings.Contains(role, "evidence") || strings.Contains(role, "snapshot"):
		return SpanEvidence, true
	case strings.Contains(role, "approval") || strings.Contains(role, "policy") || strings.Contains(role, "mutation"):
		return SpanPolicy, true
	default:
		return "", false
	}
}

func safeAttributes(attributes map[string]any) map[string]any {
	if len(attributes) == 0 {
		return nil
	}
	out := make(map[string]any, len(attributes))
	for key, value := range attributes {
		key = strings.TrimSpace(key)
		lower := strings.ToLower(key)
		if key == "" || strings.Contains(lower, "prompt") || strings.Contains(lower, "content") {
			continue
		}
		switch typed := value.(type) {
		case string:
			out[key] = redactString(typed)
		case int, int64, bool, float64:
			out[key] = typed
		default:
			out[key] = fmt.Sprintf("%v", typed)
		}
	}
	return out
}

func redactString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 {
		return value[:256] + "…"
	}
	return value
}

func fingerprint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
