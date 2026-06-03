package contractcompile

import (
	"fmt"
	"strings"

	"idiom/internal/autonomy"
)

func validatePlanExecutePlannerInput(input PlanExecutePlannerInput) error {
	if input.Runtime.EnforcementMode != "" && input.Runtime.EnforcementMode != "report_only" {
		return fmt.Errorf("passive plan-execute planner compiler only supports report_only enforcement mode, got %q", input.Runtime.EnforcementMode)
	}
	if len(input.RequiredNext) > 0 {
		return fmt.Errorf("passive plan-execute planner compiler does not accept free-form required_next directives")
	}
	for _, decision := range input.Runtime.DegradationDecisions {
		if decision.Decision == "authorized_fallback" && len(decision.AuthorizedFallbacks) == 0 {
			return fmt.Errorf("degradation decision %q authorizes fallback without naming a fallback", decision.InvariantID)
		}
	}
	for _, tc := range []struct {
		field string
		tools []string
	}{
		{field: "allowed_tools", tools: input.AllowedTools},
		{field: "forbidden_tools", tools: input.ForbiddenTools},
		{field: "required_tools", tools: input.RequiredTools},
		{field: "protocol.visible_tools", tools: input.Protocol.VisibleTools},
		{field: "protocol.callable_tools", tools: input.Protocol.CallableTools},
	} {
		for _, tool := range tc.tools {
			if !knownTool(tool) {
				return fmt.Errorf("%s contains unmapped tool %q", tc.field, tool)
			}
		}
	}
	if overlap := firstStringOverlap(input.AllowedTools, input.ForbiddenTools); overlap != "" {
		return fmt.Errorf("tool %q cannot be both allowed and forbidden", overlap)
	}
	if overlap := firstStringOverlap(input.RequiredTools, input.ForbiddenTools); overlap != "" {
		return fmt.Errorf("tool %q cannot be both required and forbidden", overlap)
	}
	if overlap := firstStringOverlap(input.Protocol.VisibleTools, input.ForbiddenTools); overlap != "" {
		return fmt.Errorf("tool %q cannot be both visible and forbidden", overlap)
	}
	if overlap := firstStringOverlap(input.Protocol.CallableTools, input.ForbiddenTools); overlap != "" {
		return fmt.Errorf("tool %q cannot be both callable and forbidden", overlap)
	}
	for _, tc := range []struct {
		field string
		tools []string
	}{
		{field: "allowed_tools", tools: input.AllowedTools},
		{field: "required_tools", tools: input.RequiredTools},
		{field: "protocol.visible_tools", tools: input.Protocol.VisibleTools},
		{field: "protocol.callable_tools", tools: input.Protocol.CallableTools},
	} {
		if exposedTool, forbiddenTool, capability := firstCapabilityOverlap(tc.tools, input.ForbiddenTools); capability != "" {
			return fmt.Errorf("%s tool %q and forbidden tool %q both map to capability %q", tc.field, exposedTool, forbiddenTool, capability)
		}
	}
	if strings.Contains(strings.ToLower(input.PermissionProfile), "read_only") {
		for _, tc := range []struct {
			field string
			tools []string
		}{
			{field: "allowed_tools", tools: input.AllowedTools},
			{field: "required_tools", tools: input.RequiredTools},
			{field: "protocol.visible_tools", tools: input.Protocol.VisibleTools},
			{field: "protocol.callable_tools", tools: input.Protocol.CallableTools},
		} {
			for _, tool := range tc.tools {
				if sideEffectForTool(tool) != autonomy.SideEffectReadOnly {
					return fmt.Errorf("read-only permission profile cannot expose mutating tool %q in %s", tool, tc.field)
				}
			}
		}
	}
	for _, tc := range []struct {
		field string
		tools []string
	}{
		{field: "allowed_tools", tools: input.AllowedTools},
		{field: "required_tools", tools: input.RequiredTools},
		{field: "protocol.visible_tools", tools: input.Protocol.VisibleTools},
		{field: "protocol.callable_tools", tools: input.Protocol.CallableTools},
	} {
		for _, tool := range tc.tools {
			if plannerForbiddenTool(tool) {
				return fmt.Errorf("plan-execute planner cannot expose tool %q in %s", tool, tc.field)
			}
		}
	}
	if err := validateActionClasses("protocol.callable_action_classes", input.Protocol.CallableActionClasses); err != nil {
		return err
	}
	if err := validateActionClasses("protocol.invalid_action_classes", input.Protocol.InvalidActionClasses); err != nil {
		return err
	}
	if overlap := firstStringOverlap(input.Protocol.CallableActionClasses, input.Protocol.InvalidActionClasses); overlap != "" {
		return fmt.Errorf("protocol action class %q cannot be both callable and invalid", overlap)
	}
	return nil
}

func plannerForbiddenTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "shell", "source_sanity", "go_diagnostics", "cpp_diagnostics", "task",
		"edit_file", "write_file", "append_file", "mkdir", "apply_patch":
		return true
	default:
		return false
	}
}

func validateProtocolProjection(protocol ModelProtocolProjection) error {
	var problems []string
	if err := validateActionClasses("model_protocol.callable_action_classes", protocol.CallableActionClasses); err != nil {
		return err
	}
	if err := validateActionClasses("model_protocol.invalid_action_classes", protocol.InvalidActionClasses); err != nil {
		return err
	}
	if overlap := firstStringOverlap(protocol.CallableActionClasses, protocol.InvalidActionClasses); overlap != "" {
		return fmt.Errorf("model protocol action class %q cannot be both callable and invalid", overlap)
	}
	for _, tool := range protocol.VisibleTools {
		if !knownTool(tool) {
			return fmt.Errorf("model_protocol.visible_tools contains unmapped tool %q", tool)
		}
	}
	for _, tool := range protocol.CallableTools {
		if !knownTool(tool) {
			return fmt.Errorf("model_protocol.callable_tools contains unmapped tool %q", tool)
		}
	}
	validateCompactProtocolLabels("model_protocol.schema_names", protocol.SchemaNames, &problems)
	validateCompactProtocolLabels("model_protocol.structured_schema_names", protocol.StructuredSchemaNames, &problems)
	validateCompactProtocolLabels("model_protocol.terminal_response_shapes", protocol.TerminalResponseShapes, &problems)
	validateProtocolProjectionRedaction("model_protocol", modelProtocolProjectionValues(protocol), &problems)
	validateCompactPacketProjection(protocol.CompactPacket, protocol.ContractID, &problems)
	if len(problems) > 0 {
		return fmt.Errorf("invalid model protocol projection: %s", strings.Join(problems, "; "))
	}
	return nil
}

func validateCompactPacketProjection(packet autonomy.ContextPacket, contractID string, problems *[]string) {
	if packet.SchemaVersion == "" && packet.PacketID == "" && len(packet.SchemaNames) == 0 {
		return
	}
	if packet.SchemaVersion != autonomy.ContextPacketSchemaVersion {
		*problems = append(*problems, "model_protocol.compact_packet.schema_version must be "+autonomy.ContextPacketSchemaVersion)
	}
	if packet.ContractID != "" && contractID != "" && packet.ContractID != contractID {
		*problems = append(*problems, "model_protocol.compact_packet.contract_id must match model_protocol.contract_id")
	}
	validateCompactProtocolLabels("model_protocol.compact_packet.schema_names", packet.SchemaNames, problems)
	validateProtocolProjectionRedaction("model_protocol.compact_packet", compactPacketProjectionValues(packet), problems)
	if err := packet.Validate(); err != nil {
		*problems = append(*problems, err.Error())
	}
}

func validateCompactProtocolLabels(field string, values []string, problems *[]string) {
	for i, value := range values {
		if !validCompactProtocolLabel(value) {
			*problems = append(*problems, fmt.Sprintf("%s[%d] must be a compact protocol label", field, i))
		}
	}
}

func validateProtocolProjectionRedaction(field string, values []string, problems *[]string) {
	for _, value := range values {
		if looksLikeRawProtocolProjectionContent(value) {
			*problems = append(*problems, field+" must contain compact redaction-safe metadata only")
			return
		}
	}
}

func modelProtocolProjectionValues(protocol ModelProtocolProjection) []string {
	values := []string{
		protocol.ContractID,
		protocol.ContractVersion,
		protocol.ProtocolHash,
		protocol.PhaseID,
		protocol.ParserMode,
		protocol.ToolTransportMode,
		protocol.ParserTransportMode,
		protocol.ParserContract,
		protocol.NativeToolPolicy,
		protocol.ToolChoice,
		protocol.ResponseFormat,
		protocol.ExpectedVisibleOutput,
		protocol.MalformedOutputPolicy,
		protocol.StaleAnchorPolicy,
		protocol.ActiveInvariantID,
	}
	values = append(values, protocol.SchemaNames...)
	values = append(values, protocol.VisibleTools...)
	values = append(values, protocol.CallableTools...)
	values = append(values, protocol.CallableActionClasses...)
	values = append(values, protocol.InvalidActionClasses...)
	values = append(values, protocol.TerminalResponseShapes...)
	values = append(values, protocol.StructuredSchemaNames...)
	values = append(values, protocol.SourceBodyObligations...)
	values = append(values, protocol.RequiredEditCoverage...)
	values = append(values, protocol.PathRoleSummary...)
	values = append(values, protocol.VerificationObligations...)
	values = append(values, protocol.SafeAlternatives...)
	values = append(values, protocol.RequiredTools...)
	values = append(values, protocol.ReadinessGate...)
	values = append(values, protocol.Constraints...)
	values = append(values, protocol.InvalidActions...)
	return values
}

func compactPacketProjectionValues(packet autonomy.ContextPacket) []string {
	values := []string{packet.SchemaVersion, packet.PacketID, packet.ContractID, packet.ProtocolHash}
	values = append(values, packet.SummaryRefs...)
	values = append(values, packet.TargetRefs...)
	values = append(values, packet.PathRefs...)
	values = append(values, packet.SymbolRefs...)
	values = append(values, packet.EvidenceRefs...)
	values = append(values, packet.ObligationRefs...)
	values = append(values, packet.SchemaNames...)
	values = append(values, packet.RedactionMarkers...)
	return values
}

func validCompactProtocolLabel(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 160 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '_', '-', '/', '.', ':', '=', '@', '<', '>', ' ':
			continue
		default:
			return false
		}
	}
	return true
}

func looksLikeRawProtocolProjectionContent(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	lowered := strings.ToLower(trimmed)
	for _, marker := range []string{
		"\n",
		"raw_prompt",
		"raw prompt",
		"raw_transcript",
		"raw transcript",
		"transcript:",
		"file_body",
		"file body",
		"old_text",
		"new_text",
		"-----begin",
		"api_key",
		"secret=",
		"password=",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

func validateActionClasses(field string, values []string) error {
	for _, value := range values {
		actionClass := autonomy.CanonicalActionClass(autonomy.ActionClass(value))
		if !autonomy.ValidActionClass(actionClass) {
			return fmt.Errorf("%s contains invalid action class %q", field, value)
		}
	}
	return nil
}

func firstStringOverlap(left []string, right []string) string {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[strings.TrimSpace(value)] = struct{}{}
	}
	for _, value := range left {
		value = strings.TrimSpace(value)
		if _, ok := rightSet[value]; ok && value != "" {
			return value
		}
	}
	return ""
}

func firstCapabilityOverlap(exposedTools []string, forbiddenTools []string) (string, string, autonomy.Capability) {
	for _, exposedTool := range exposedTools {
		for _, exposedCapability := range capabilitiesForTool(exposedTool) {
			for _, forbiddenTool := range forbiddenTools {
				for _, forbiddenCapability := range capabilitiesForTool(forbiddenTool) {
					if exposedCapability != "" && exposedCapability == forbiddenCapability {
						return exposedTool, forbiddenTool, exposedCapability
					}
				}
			}
		}
	}
	return "", "", ""
}
