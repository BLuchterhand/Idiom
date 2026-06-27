package contractcompile

import (
	"fmt"
	"strings"

	"idiom/internal/contracts/autonomy"
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

	if err := validateToolVocabulary(toolVocabularyPolicy{
		ForbiddenTools: input.ForbiddenTools,
		ExposedToolSets: []toolVocabularySet{
			{Role: "allowed", Field: "allowed_tools", Tools: input.AllowedTools},
			{Role: "required", Field: "required_tools", Tools: input.RequiredTools},
			{Role: "visible", Field: "protocol.visible_tools", Tools: input.Protocol.VisibleTools},
			{Role: "callable", Field: "protocol.callable_tools", Tools: input.Protocol.CallableTools},
		},
		RequireCallableVisible: input.Protocol.CallableToolsSpecified,
		PlannerOnly:            true,
		ReadOnlyProfile:        strings.Contains(strings.ToLower(input.PermissionProfile), "read_only"),
	}); err != nil {
		return err
	}
	if err := validateHiddenToolProjection("protocol.hidden_tools", input.Protocol.HiddenTools, input.Protocol.VisibleTools, input.Protocol.CallableTools); err != nil {
		return err
	}

	if err := validateActionClassVocabulary(actionClassVocabulary{
		CallableField: "protocol.callable_action_classes",
		Callable:      input.Protocol.CallableActionClasses,
		InvalidField:  "protocol.invalid_action_classes",
		Invalid:       input.Protocol.InvalidActionClasses,
		ErrorPrefix:   "protocol",
	}); err != nil {
		return err
	}

	return nil
}

func plannerForbiddenTool(tool string) bool {
	return !autonomy.IsPlanExecutePlannerToolAllowed(tool)
}

func validateProtocolProjection(protocol ModelProtocolProjection) error {
	var problems []string

	if err := validateActionClassVocabulary(actionClassVocabulary{
		CallableField: "model_protocol.callable_action_classes",
		Callable:      protocol.CallableActionClasses,
		InvalidField:  "model_protocol.invalid_action_classes",
		Invalid:       protocol.InvalidActionClasses,
		ErrorPrefix:   "model protocol",
	}); err != nil {
		return err
	}

	if err := validateToolVocabulary(toolVocabularyPolicy{
		ExposedToolSets: []toolVocabularySet{
			{Role: "required", Field: "model_protocol.required_tools", Tools: protocol.RequiredTools},
			{Role: "visible", Field: "model_protocol.visible_tools", Tools: protocol.VisibleTools},
			{Role: "callable", Field: "model_protocol.callable_tools", Tools: protocol.CallableTools},
		},
		RequireCallableVisible: true,
		PlannerOnly:            true,
	}); err != nil {
		return err
	}
	if err := validateHiddenToolProjection("model_protocol.hidden_tools", protocol.HiddenTools, protocol.VisibleTools, protocol.CallableTools); err != nil {
		return err
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

type toolVocabularyPolicy struct {
	ForbiddenTools         []string
	ExposedToolSets        []toolVocabularySet
	RequireCallableVisible bool
	PlannerOnly            bool
	ReadOnlyProfile        bool
}

type toolVocabularySet struct {
	Role  string
	Field string
	Tools []string
}

func validateToolVocabulary(policy toolVocabularyPolicy) error {
	if err := validateKnownTools("forbidden_tools", policy.ForbiddenTools); err != nil {
		return err
	}

	var visible []string
	var callable []string

	for _, set := range policy.ExposedToolSets {
		if err := validateKnownTools(set.Field, set.Tools); err != nil {
			return err
		}

		if overlap := firstStringOverlap(set.Tools, policy.ForbiddenTools); overlap != "" {
			return fmt.Errorf("tool %q cannot be both %s and forbidden", overlap, set.Role)
		}

		if exposedTool, forbiddenTool, capability := firstCapabilityOverlap(set.Tools, policy.ForbiddenTools); capability != "" {
			return fmt.Errorf("%s tool %q and forbidden tool %q both map to capability %q", set.Field, exposedTool, forbiddenTool, capability)
		}

		if policy.ReadOnlyProfile {
			for _, tool := range set.Tools {
				if sideEffectForTool(tool) != autonomy.SideEffectReadOnly {
					return fmt.Errorf("read-only permission profile cannot expose mutating tool %q in %s", tool, set.Field)
				}
			}
		}

		if policy.PlannerOnly {
			for _, tool := range set.Tools {
				if plannerForbiddenTool(tool) {
					return fmt.Errorf("plan-execute planner cannot expose tool %q in %s", tool, set.Field)
				}
			}
		}

		switch set.Role {
		case "visible":
			visible = set.Tools
		case "callable":
			callable = set.Tools
		}
	}

	if policy.RequireCallableVisible {
		if missing := firstMissingTool(callable, visible); missing != "" {
			return fmt.Errorf("tool %q cannot be callable without being visible", missing)
		}
	}

	return nil
}

func validateKnownTools(field string, tools []string) error {
	for _, tool := range tools {
		if !knownTool(tool) {
			return fmt.Errorf("%s contains unmapped tool %q", field, tool)
		}
	}

	return nil
}

func validateHiddenToolProjection(field string, hiddenTools []string, visibleTools []string, callableTools []string) error {
	if err := validateKnownTools(field, hiddenTools); err != nil {
		return err
	}

	if overlap := firstStringOverlap(hiddenTools, visibleTools); overlap != "" {
		return fmt.Errorf("tool %q cannot be both hidden and visible in %s", overlap, field)
	}

	if overlap := firstStringOverlap(hiddenTools, callableTools); overlap != "" {
		return fmt.Errorf("tool %q cannot be both hidden and callable in %s", overlap, field)
	}

	return nil
}

type actionClassVocabulary struct {
	CallableField string
	Callable      []string
	InvalidField  string
	Invalid       []string
	ErrorPrefix   string
}

func validateActionClassVocabulary(vocabulary actionClassVocabulary) error {
	if err := validateActionClasses(vocabulary.CallableField, vocabulary.Callable); err != nil {
		return err
	}

	if err := validateActionClasses(vocabulary.InvalidField, vocabulary.Invalid); err != nil {
		return err
	}

	if overlap := firstStringOverlap(vocabulary.Callable, vocabulary.Invalid); overlap != "" {
		return fmt.Errorf("%s action class %q cannot be both callable and invalid", vocabulary.ErrorPrefix, overlap)
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
	values = append(values, protocol.HiddenTools...)
	values = append(values, protocol.CallableTools...)
	values = append(values, protocol.CallableActionClasses...)
	values = append(values, protocol.InvalidActionClasses...)
	values = append(values, protocol.TerminalResponseShapes...)
	values = append(values, protocol.StructuredSchemaNames...)
	values = append(values, protocol.SourceBodyObligations...)
	values = append(values, protocol.CompletedGitHistoryPaths...)
	values = append(values, protocol.PendingGitHistoryPaths...)
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

func firstMissingTool(tools []string, available []string) string {
	availableSet := make(map[string]struct{}, len(available))
	for _, tool := range available {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			availableSet[tool] = struct{}{}
		}
	}

	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}

		if _, ok := availableSet[tool]; !ok {
			return tool
		}
	}

	return ""
}
