package featureplanner

import (
	"fmt"
	"strings"

	"idiom/internal/platform/sessionid"
)

// PlanExecuteInvocationBinding is the parent feature-atom identity carried in
// the trusted header of a nested plan-execute prompt. The terminal completion
// must retain this correlation; its own scope-contract identity is not a
// substitute for the parent atom contract snapshot.
type PlanExecuteInvocationBinding struct {
	SessionID    string
	ContractID   string
	SnapshotHash string
}

// ParsePlanExecuteInvocationBinding reads only the trusted prompt header that
// BuildImplementorPromptFromContract emits. It stops before the atom brief so
// user/model prose cannot mint or override parent contract identity.
func ParsePlanExecuteInvocationBinding(prompt string) (PlanExecuteInvocationBinding, error) {
	var binding PlanExecuteInvocationBinding

	for _, raw := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "This atom brief is consumed by plan-execute.") {
			break
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)

		switch key {
		case "plan-execute session id":
			if value != "" && !sessionid.Valid(value) {
				return PlanExecuteInvocationBinding{}, fmt.Errorf("invalid plan-execute session id %q", value)
			}
			if err := bindPlanExecuteInvocationValue(&binding.SessionID, value, key); err != nil {
				return PlanExecuteInvocationBinding{}, err
			}
		case "active plan contract id":
			if err := bindPlanExecuteInvocationValue(&binding.ContractID, value, key); err != nil {
				return PlanExecuteInvocationBinding{}, err
			}
		case "active plan contract snapshot hash":
			if err := bindPlanExecuteInvocationValue(&binding.SnapshotHash, value, key); err != nil {
				return PlanExecuteInvocationBinding{}, err
			}
		}
	}

	return binding, nil
}

func bindPlanExecuteInvocationValue(target *string, value string, field string) error {
	if value == "" {
		return fmt.Errorf("%s is empty", field)
	}
	if *target != "" && *target != value {
		return fmt.Errorf("%s appears with conflicting values", field)
	}
	*target = value
	return nil
}

// CorrelateCompletion fills missing parent identity from the trusted
// invocation binding. Conflicting explicit completion identity is preserved so
// the feature-planner contract validator rejects it rather than hiding it.
func (binding PlanExecuteInvocationBinding) CorrelateCompletion(summary PlanExecuteCompletionSummary) PlanExecuteCompletionSummary {
	if strings.TrimSpace(summary.ContractID) == "" {
		summary.ContractID = strings.TrimSpace(binding.ContractID)
	}
	if strings.TrimSpace(summary.SnapshotHash) == "" {
		summary.SnapshotHash = strings.TrimSpace(binding.SnapshotHash)
	}
	if summary.terminalRecord != nil {
		record := *summary.terminalRecord
		if strings.TrimSpace(record.ContractID) == "" {
			record.ContractID = strings.TrimSpace(binding.ContractID)
		}
		if strings.TrimSpace(record.SnapshotHash) == "" {
			record.SnapshotHash = strings.TrimSpace(binding.SnapshotHash)
		}
		summary.terminalRecord = &record
	}

	return summary
}
