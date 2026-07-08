package workspace

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/textutil"
)

const ToolSidecarEnvelopeVersion = "idiom.tool.v1"
const ToolSidecarChildContextVersion = "idiom.sidecar_child_context.v1"
const maxToolSidecarErrorMessageBytes = 500

type ToolSidecarEnvelope struct {
	SchemaVersion    string                    `json:"schema_version"`
	Tool             string                    `json:"tool"`
	Profile          string                    `json:"profile"`
	Caller           ToolSidecarCaller         `json:"caller"`
	Contract         ToolSidecarContract       `json:"contract"`
	Freshness        any                       `json:"freshness"`
	Warnings         []ToolSidecarWarning      `json:"warnings"`
	AuditEventIDs    []string                  `json:"audit_event_ids"`
	StructuredOutput any                       `json:"structured_output"`
	Error            *ToolSidecarError         `json:"error"`
	ChildContext     *ToolSidecarChildContext  `json:"child_context,omitempty"`
	Metadata         autonomy.ContractMetadata `json:"metadata,omitempty"`
}

type ToolSidecarOptions struct {
	Caller       ToolSidecarCaller
	Transport    string
	ChildContext *ToolSidecarChildContext
}

type ToolSidecarCaller struct {
	CallerID  string `json:"caller_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

type ToolSidecarContract struct {
	ID                string                    `json:"id"`
	Kind              string                    `json:"kind"`
	Tool              string                    `json:"tool"`
	Profile           string                    `json:"profile"`
	SchemaVersion     string                    `json:"schema_version"`
	Transport         string                    `json:"transport"`
	DecisionAuthority string                    `json:"decision_authority,omitempty"`
	Policy            ToolSchemaPolicyExport    `json:"policy,omitempty"`
	Metadata          autonomy.ContractMetadata `json:"metadata,omitempty"`
}

type ToolSidecarChildContext struct {
	SchemaVersion    string `json:"schema_version"`
	ParentContractID string `json:"parent_contract_id,omitempty"`
	ChildContractID  string `json:"child_contract_id,omitempty"`
	InvocationID     string `json:"invocation_id,omitempty"`
	TerminalID       string `json:"terminal_id,omitempty"`
	AcceptanceID     string `json:"acceptance_id,omitempty"`
	Role             string `json:"role,omitempty"`
	Lane             string `json:"lane,omitempty"`
	Phase            string `json:"phase,omitempty"`
	WorkspaceMode    string `json:"workspace_mode,omitempty"`
	PackID           string `json:"pack_id,omitempty"`
}

type ToolSidecarWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ToolSidecarError struct {
	Class   string `json:"class"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func BuildToolSidecarEnvelopeWithOptions(req ToolRequest, result ToolResult, profile string, opts ToolSidecarOptions) ToolSidecarEnvelope {
	tool := textutil.FirstNonEmpty(strings.TrimSpace(result.Tool), strings.TrimSpace(req.Tool))

	profile = textutil.FirstNonEmpty(strings.TrimSpace(profile), "default")

	structuredOutput := toolSidecarStructuredOutput(result.Output)
	outputObject := toolSidecarOutputObject(structuredOutput)
	transport := normalizeToolSidecarTransport(opts.Transport)
	contract := BuildToolSidecarContract(tool, profile, transport)
	childContext := normalizeToolSidecarChildContext(opts.ChildContext)

	return ToolSidecarEnvelope{
		SchemaVersion:    ToolSidecarEnvelopeVersion,
		Tool:             tool,
		Profile:          profile,
		Caller:           opts.Caller,
		Contract:         contract,
		Freshness:        toolSidecarFreshness(outputObject),
		Warnings:         toolSidecarWarnings(outputObject),
		AuditEventIDs:    toolSidecarAuditEventIDs(outputObject),
		StructuredOutput: structuredOutput,
		Error:            toolSidecarError(result),
		ChildContext:     childContext,
		Metadata:         contract.Metadata,
	}
}

func normalizeToolSidecarChildContext(input *ToolSidecarChildContext) *ToolSidecarChildContext {
	if input == nil {
		return nil
	}

	context := *input
	context.SchemaVersion = ToolSidecarChildContextVersion

	var err error
	if context.ParentContractID, err = normalizeSidecarCallerField("child_context.parent_contract_id", context.ParentContractID); err != nil {
		return nil
	}
	if context.ChildContractID, err = normalizeSidecarCallerField("child_context.child_contract_id", context.ChildContractID); err != nil {
		return nil
	}
	if context.InvocationID, err = normalizeSidecarCallerField("child_context.invocation_id", context.InvocationID); err != nil {
		return nil
	}
	if context.TerminalID, err = normalizeSidecarCallerField("child_context.terminal_id", context.TerminalID); err != nil {
		return nil
	}
	if context.AcceptanceID, err = normalizeSidecarCallerField("child_context.acceptance_id", context.AcceptanceID); err != nil {
		return nil
	}
	if context.Role, err = normalizeSidecarCallerField("child_context.role", context.Role); err != nil {
		return nil
	}
	if context.Lane, err = normalizeSidecarCallerField("child_context.lane", context.Lane); err != nil {
		return nil
	}
	if context.Phase, err = normalizeSidecarCallerField("child_context.phase", context.Phase); err != nil {
		return nil
	}
	if context.WorkspaceMode, err = normalizeSidecarCallerField("child_context.workspace_mode", context.WorkspaceMode); err != nil {
		return nil
	}
	if context.PackID, err = normalizeSidecarCallerField("child_context.pack_id", context.PackID); err != nil {
		return nil
	}

	if context.ParentContractID == "" &&
		context.ChildContractID == "" &&
		context.InvocationID == "" &&
		context.TerminalID == "" &&
		context.AcceptanceID == "" &&
		context.Role == "" &&
		context.Lane == "" &&
		context.Phase == "" &&
		context.WorkspaceMode == "" &&
		context.PackID == "" {

		return nil
	}

	return &context
}

func NewToolSidecarCaller(callerID string, requestID string, traceID string) (ToolSidecarCaller, error) {
	caller := ToolSidecarCaller{}

	var err error

	if caller.CallerID, err = normalizeSidecarCallerField("caller-id", callerID); err != nil {
		return ToolSidecarCaller{}, err
	}

	if caller.RequestID, err = normalizeSidecarCallerField("request-id", requestID); err != nil {
		return ToolSidecarCaller{}, err
	}

	if caller.TraceID, err = normalizeSidecarCallerField("trace-id", traceID); err != nil {
		return ToolSidecarCaller{}, err
	}

	return caller, nil
}

func normalizeSidecarCallerField(name string, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	if len(value) > 128 {
		return "", fmt.Errorf("%s must be 128 bytes or fewer", name)
	}

	lower := strings.ToLower(value)
	for _, prefix := range []string{"sk-", "sk_", "ghp_", "github_pat_", "xoxb-"} {
		if strings.HasPrefix(lower, prefix) {
			return "", fmt.Errorf("%s must be a short opaque identifier, not a credential-shaped value", name)
		}
	}

	for _, forbidden := range []string{"secret", "token", "password", "passwd", "api_key", "apikey", "private_key", "credential"} {
		if strings.Contains(lower, forbidden) {
			return "", fmt.Errorf("%s must be an opaque non-secret identifier", name)
		}
	}

	if looksLikePathOrPrivateLocator(value) {
		return "", fmt.Errorf("%s must be a short opaque identifier, not a path-shaped value", name)
	}

	if strings.Contains(value, "@") {
		return "", fmt.Errorf("%s must be a short opaque identifier, not an email address", name)
	}

	if looksLikeJWT(value) || looksLikeLongHexOrBase64Blob(value) {
		return "", fmt.Errorf("%s must be a short opaque identifier, not a token-shaped value", name)
	}

	for _, r := range value {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ':') {
			return "", fmt.Errorf("%s may contain only ASCII letters, digits, '.', '-', '_', or ':'", name)
		}
	}

	return value, nil
}

func looksLikePathOrPrivateLocator(value string) bool {
	lower := strings.ToLower(value)
	if strings.HasPrefix(value, "~") || strings.Contains(value, "/") || strings.Contains(value, `\`) {
		return true
	}

	if strings.Contains(value, "..") {
		return true
	}

	for _, marker := range []string{".ssh", ".aws", ".config", ".env", "id_rsa", "id_ed25519"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}

func looksLikeJWT(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || len(value) < 24 {
		return false
	}

	for _, part := range parts {
		if len(part) < 6 || !allRunesInSet(part, isBase64URLRune) {
			return false
		}
	}

	return true
}

func looksLikeLongHexOrBase64Blob(value string) bool {
	if len(value) >= 32 && allRunesInSet(value, isHexRune) {
		return true
	}

	if len(value) >= 48 && allRunesInSet(value, isBase64LikeRune) {
		return true
	}

	return false
}

func allRunesInSet(value string, allowed func(rune) bool) bool {
	for _, r := range value {
		if r > unicode.MaxASCII || !allowed(r) {
			return false
		}
	}

	return value != ""
}

func isHexRune(r rune) bool {
	return unicode.IsDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isBase64URLRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_'
}

func isBase64LikeRune(r rune) bool {
	return isBase64URLRune(r) || r == '+' || r == '/' || r == '='
}

func normalizeToolSidecarTransport(transport string) string {
	transport = strings.TrimSpace(transport)
	if transport == "" {
		return "cli"
	}

	return transport
}

func BuildToolSidecarContract(tool string, profile string, transport string) ToolSidecarContract {
	tool = strings.TrimSpace(tool)
	profile = textutil.FirstNonEmpty(strings.TrimSpace(profile), ToolProfileDefault)
	transport = normalizeToolSidecarTransport(transport)
	metadata := autonomy.DefaultContractMetadata("p7.1", "tool_sidecar_envelope")
	policy := toolSchemaPolicyForTool(tool)

	return ToolSidecarContract{
		ID:                ToolSidecarContractID(tool),
		Kind:              "workspace_tool_result",
		Tool:              tool,
		Profile:           profile,
		SchemaVersion:     ToolSchemaExportVersion,
		Transport:         transport,
		DecisionAuthority: policy.DecisionAuthority,
		Policy:            policy,
		Metadata:          metadata,
	}
}

func ToolSidecarContractID(tool string) string {
	return "idiom.tool.v1:" + strings.TrimSpace(tool)
}

func toolSidecarStructuredOutput(output string) any {
	trimmed := strings.TrimSpace(output)

	var decoded any

	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return decoded
		}
	}

	return map[string]any{
		"format": "text",
		"text":   output,
	}
}

func toolSidecarOutputObject(structuredOutput any) map[string]any {
	object, ok := structuredOutput.(map[string]any)
	if !ok {
		return nil
	}

	if format, _ := object["format"].(string); format == "text" {
		return nil
	}

	return object
}

func toolSidecarFreshness(output map[string]any) any {
	if output == nil {
		return nil
	}

	for _, key := range []string{"freshness", "context_freshness"} {
		if value, ok := output[key]; ok {
			return value
		}
	}

	if contract, ok := output["contract"].(map[string]any); ok {
		if value, ok := contract["freshness"]; ok {
			return value
		}
	}

	return nil
}

func toolSidecarWarnings(output map[string]any) []ToolSidecarWarning {
	warnings := make([]ToolSidecarWarning, 0)
	if output == nil {
		return warnings
	}

	if rawWarnings, ok := output["warnings"].([]any); ok {
		for _, raw := range rawWarnings {
			switch value := raw.(type) {
			case string:
				if message := strings.TrimSpace(value); message != "" {
					warnings = append(warnings, ToolSidecarWarning{Code: "tool_warning", Message: message})
				}
			case map[string]any:
				message := textutil.FirstNonEmpty(stringFromAny(value["message"]), stringFromAny(value["summary"]), stringFromAny(value["reason"]))
				if message != "" {
					warnings = append(warnings, ToolSidecarWarning{
						Code:    textutil.FirstNonEmpty(stringFromAny(value["code"]), "tool_warning"),
						Message: message,
					})
				}
			}
		}
	}

	return warnings
}

func toolSidecarAuditEventIDs(output map[string]any) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0)

	var walk func(any)

	walk = func(value any) {
		if len(ids) >= 100 {
			return
		}

		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "event_id" || key == "audit_event_id" {
					if id := stringFromAny(child); id != "" {
						if _, ok := seen[id]; !ok {
							seen[id] = struct{}{}
							ids = append(ids, id)
						}
					}
				}

				if key == "audit_event_ids" {
					appendAuditEventIDStrings(child, seen, &ids)
				}

				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(output)
	slices.Sort(ids)

	return ids
}

func toolSidecarError(result ToolResult) *ToolSidecarError {
	message := strings.TrimSpace(result.Error)
	code := classifyToolSidecarError(result)

	if message == "" && classifyTypedToolSidecarFailure(result.Failure) != "" {
		message = strings.TrimSpace(result.Failure.Error())
	}

	if message == "" || code == "" {
		return nil
	}

	return &ToolSidecarError{
		Class:   code,
		Code:    code,
		Message: boundedToolSidecarErrorMessage(message),
	}
}

func appendAuditEventIDStrings(value any, seen map[string]struct{}, ids *[]string) {
	values, ok := value.([]any)
	if !ok {
		return
	}

	for _, raw := range values {
		if len(*ids) >= 100 {
			return
		}

		id := stringFromAny(raw)
		if id == "" {
			continue
		}

		if _, ok := seen[id]; ok {
			continue
		}

		seen[id] = struct{}{}
		*ids = append(*ids, id)
	}
}

func boundedToolSidecarErrorMessage(message string) string {
	if len(message) <= maxToolSidecarErrorMessageBytes {
		return message
	}

	var builder strings.Builder

	for _, r := range message {
		if builder.Len()+len(string(r)) > maxToolSidecarErrorMessageBytes {
			break
		}

		builder.WriteRune(r)
	}

	return strings.TrimSpace(builder.String()) + "...(truncated)"
}

func classifyToolSidecarError(result ToolResult) string {
	if code := classifyToolSidecarResultCode(result.Code); code != "" {
		return code
	}

	if code := classifyTypedToolSidecarFailure(result.Failure); code != "" {
		return code
	}

	return "tool_error"
}

func classifyToolSidecarResultCode(code string) string {
	switch strings.TrimSpace(code) {
	case "profile_denied", "read_only_profile":
		return strings.TrimSpace(code)
	default:
		return ""
	}
}

func classifyTypedToolSidecarFailure(failure error) string {
	if failure == nil {
		return ""
	}

	switch failures.ReasonOf(failure) {
	case reasonContextFreshnessBlocker:
		return "stale_context"
	case reasonToolUnsupported:
		return "unsupported_tool"
	case reasonToolPermissionPolicy, reasonToolWriteDisabled:
		return "permission_policy"
	case reasonToolInvalidArgument, reasonToolPathResolutionFailed, reasonPlanArtifactSchemaInvalid:
		return "invalid_input"
	case reasonToolUnavailable, reasonPythonLSPUnavailable, reasonGitHistoryUnavailable:
		return "backend_unavailable"
	}

	switch failures.CodeOf(failure) {
	case failures.Unknown:
		return ""
	case failures.InvalidArgument, failures.OutOfRange:
		return "invalid_input"
	case failures.PermissionDenied, failures.Unauthenticated:
		return "permission_policy"
	case failures.Unimplemented:
		return "unsupported_tool"
	case failures.DeadlineExceeded:
		return "timeout"
	case failures.Unavailable, failures.NotFound:
		return "backend_unavailable"
	case failures.Internal, failures.DataLoss, failures.Aborted, failures.ResourceExhausted, failures.FailedPrecondition, failures.Canceled, failures.AlreadyExists:
		return "tool_failed"
	default:
		return "tool_error"
	}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}
