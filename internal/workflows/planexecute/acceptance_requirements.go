package planexecute

import (
	"encoding/json"
	"strings"

	"idiom/internal/contracts/plan"
)

const (
	planExecuteTypedPlanContractMarker = "Typed plan execution contract:"
	planExecuteTypedAtomContractMarker = "Typed plan authority projection:"
)

func planExecuteRequiredAcceptanceEvidenceIDsFromPrompt(prompt string) []string {
	if body := planExecuteJSONAfterPromptMarker(prompt, planExecuteTypedAtomContractMarker); body != "" {
		var atom plancontract.AtomExecutionContract
		if err := json.Unmarshal([]byte(body), &atom); err == nil {
			if ids := planExecuteRequiredAcceptanceEvidenceIDsFromAtom(atom); len(ids) > 0 {
				return ids
			}
		}
	}
	if body := planExecuteJSONAfterPromptMarker(prompt, planExecuteTypedPlanContractMarker); body != "" {
		var contract plancontract.PlanExecutionContract
		if err := json.Unmarshal([]byte(body), &contract); err == nil {
			atoms := contract.Normalized().Atoms
			if len(atoms) == 1 {
				return planExecuteRequiredAcceptanceEvidenceIDsFromAtom(atoms[0])
			}
		}
	}
	return nil
}

func planExecuteRequiredAcceptanceEvidenceIDsFromAtom(atom plancontract.AtomExecutionContract) []string {
	var ids []string
	for _, obligation := range atom.Normalized().AcceptanceObligations {
		if !obligation.Required || obligation.ID == "" {
			continue
		}
		ids = append(ids, obligation.ID)
	}
	return normalizePlanExecuteTerminalStableIDs(ids)
}

func planExecuteJSONAfterPromptMarker(prompt string, marker string) string {
	idx := strings.Index(prompt, marker)
	if idx < 0 {
		return ""
	}
	start := strings.Index(prompt[idx+len(marker):], "{")
	if start < 0 {
		return ""
	}
	start += idx + len(marker)
	end := planExecuteJSONObjectEnd(prompt, start)
	if end <= start {
		return ""
	}
	return strings.TrimSpace(prompt[start:end])
}

func planExecuteJSONObjectEnd(value string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(value); i++ {
		ch := value[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
