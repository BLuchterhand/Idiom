package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"idiom/internal/contracts/toolpolicy"
)

type ToolExecutionStatus string

const (
	ToolExecutionReceived  ToolExecutionStatus = "received"
	ToolExecutionAdmitted  ToolExecutionStatus = "admitted"
	ToolExecutionDenied    ToolExecutionStatus = "denied"
	ToolExecutionApproval  ToolExecutionStatus = "approval_required"
	ToolExecutionRunning   ToolExecutionStatus = "running"
	ToolExecutionCompleted ToolExecutionStatus = "completed"
	ToolExecutionFailed    ToolExecutionStatus = "failed"
	ToolExecutionCanceled  ToolExecutionStatus = "canceled"
)

// ToolExecutionEnvelope is the transport-neutral lifecycle record for one
// logical tool call. It contains no raw request body or result output.
type ToolExecutionEnvelope struct {
	SchemaVersion      string              `json:"schema_version"`
	InvocationID       string              `json:"invocation_id,omitempty"`
	Transport          string              `json:"transport"`
	RequestFingerprint string              `json:"request_fingerprint"`
	Tool               string              `json:"tool"`
	PreflightDecision  toolpolicy.Decision `json:"preflight_decision"`
	ApprovalRequired   bool                `json:"approval_required,omitempty"`
	Status             ToolExecutionStatus `json:"status"`
	Canceled           bool                `json:"canceled,omitempty"`
	ResultCode         string              `json:"result_code,omitempty"`
	ResultFailed       bool                `json:"result_failed,omitempty"`
	EvidencePaths      []string            `json:"evidence_paths,omitempty"`
	EvidenceProduced   bool                `json:"evidence_produced"`
	VerificationStatus string              `json:"verification_status"`
}

const toolExecutionSchemaVersion = "idiom.tool_execution_envelope.v1"

type toolTransportContextKey struct{}

func WithToolTransport(ctx context.Context, transport string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolTransportContextKey{}, strings.TrimSpace(transport))
}

func toolTransportFromContext(ctx context.Context) string {
	if ctx == nil {
		return "workspace"
	}
	transport, _ := ctx.Value(toolTransportContextKey{}).(string)
	if strings.TrimSpace(transport) == "" {
		return "workspace"
	}
	return strings.TrimSpace(transport)
}

func NewToolExecutionEnvelope(transport string, req ToolRequest) ToolExecutionEnvelope {
	return ToolExecutionEnvelope{
		SchemaVersion:      toolExecutionSchemaVersion,
		InvocationID:       strings.TrimSpace(req.ID),
		Transport:          strings.TrimSpace(transport),
		RequestFingerprint: toolRequestFingerprint(req),
		Tool:               strings.TrimSpace(req.Tool),
		Status:             ToolExecutionReceived,
		VerificationStatus: "unverified",
	}
}

// ToolRequestFingerprint returns the metadata-only identity used by execution
// envelopes and trace correlation.
func ToolRequestFingerprint(req ToolRequest) string { return toolRequestFingerprint(req) }

type toolExecutionObserverContextKey struct{}

func WithToolExecutionObserver(ctx context.Context, observer func(ToolExecutionEnvelope)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolExecutionObserverContextKey{}, observer)
}

func toolExecutionObserverFromContext(ctx context.Context) func(ToolExecutionEnvelope) {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(toolExecutionObserverContextKey{}).(func(ToolExecutionEnvelope))
	return observer
}

func (e ToolExecutionEnvelope) Validate() error {
	if e.SchemaVersion != toolExecutionSchemaVersion {
		return &toolExecutionError{"unsupported tool execution envelope schema"}
	}
	if strings.TrimSpace(e.Transport) == "" || strings.TrimSpace(e.Tool) == "" || strings.TrimSpace(e.RequestFingerprint) == "" {
		return &toolExecutionError{"tool execution envelope is missing transport, tool, or request fingerprint"}
	}
	return nil
}

type toolExecutionError struct{ message string }

func (e *toolExecutionError) Error() string { return e.message }

func toolRequestFingerprint(req ToolRequest) string {
	// Only stable, non-body routing fields participate. Content, prompts, old
	// text, new text, and commands are intentionally excluded from evidence.
	shape := struct {
		Tool       string `json:"tool"`
		ID         string `json:"id,omitempty"`
		Path       string `json:"path,omitempty"`
		Root       string `json:"root,omitempty"`
		Workdir    string `json:"workdir,omitempty"`
		Capability string `json:"capability,omitempty"`
	}{req.Tool, req.ID, req.Path, req.Root, req.Workdir, req.Capability}
	data, _ := json.Marshal(shape)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func toolExecutionPaths(result ToolResult) []string {
	paths := append([]string(nil), result.Paths...)
	paths = append(paths, result.ChangedPaths...)
	sort.Strings(paths)
	out := paths[:0]
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || (len(out) > 0 && out[len(out)-1] == path) {
			continue
		}
		out = append(out, path)
	}
	return out
}

func (e *ToolExecutionEnvelope) applyResult(result ToolResult, ctxCanceled bool) {
	if e == nil {
		return
	}
	e.ResultCode = strings.TrimSpace(result.Code)
	e.ResultFailed = strings.TrimSpace(result.Error) != "" || result.Failure != nil
	e.EvidencePaths = toolExecutionPaths(result)
	e.EvidenceProduced = result.Facts != nil || len(e.EvidencePaths) > 0
	if result.Facts != nil && len(result.Facts.VerificationEvidence) > 0 {
		e.VerificationStatus = "observed"
	}
	e.Canceled = ctxCanceled
	if e.Status != ToolExecutionDenied && e.Status != ToolExecutionApproval {
		switch {
		case ctxCanceled:
			e.Status = ToolExecutionCanceled
		case e.ResultFailed:
			e.Status = ToolExecutionFailed
		default:
			e.Status = ToolExecutionCompleted
		}
	}
}
