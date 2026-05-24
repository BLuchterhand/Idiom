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
	for _, tc := range []struct {
		field string
		tools []string
	}{
		{field: "allowed_tools", tools: input.AllowedTools},
		{field: "forbidden_tools", tools: input.ForbiddenTools},
		{field: "required_tools", tools: input.RequiredTools},
		{field: "protocol.visible_tools", tools: input.Protocol.VisibleTools},
	} {
		for _, tool := range tc.tools {
			if !knownTool(tool) {
				return fmt.Errorf("%s contains unmapped tool %q", tc.field, tool)
			}
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
		} {
			for _, tool := range tc.tools {
				if sideEffectForTool(tool) != autonomy.SideEffectReadOnly {
					return fmt.Errorf("read-only permission profile cannot expose mutating tool %q in %s", tool, tc.field)
				}
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
