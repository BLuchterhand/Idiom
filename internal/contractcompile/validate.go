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
	return nil
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
