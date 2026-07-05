package plan

import (
	"encoding/json"
	"sort"
	"strings"
)

const (
	PromptPlanExecutionContractMarker = "Typed plan execution contract:"
	PromptAtomExecutionContractMarker = "Typed plan authority projection:"
)

func PlanExecutionContractFromPrompt(prompt string) (PlanExecutionContract, bool) {
	var contract PlanExecutionContract
	if !decodePromptJSONAfterMarker(prompt, PromptPlanExecutionContractMarker, &contract) {
		return PlanExecutionContract{}, false
	}

	return contract, true
}

func AtomExecutionContractFromPrompt(prompt string) (AtomExecutionContract, bool) {
	var atom AtomExecutionContract
	if !decodePromptJSONAfterMarker(prompt, PromptAtomExecutionContractMarker, &atom) {
		return AtomExecutionContract{}, false
	}

	return atom, true
}

func AuthorizedNewFilePathsFromPrompt(prompt string) []string {
	contract, ok := PlanExecutionContractFromPrompt(prompt)
	if !ok {
		return nil
	}

	var paths []string
	for _, file := range contract.Normalized().AuthorizedNewFiles {
		if path := NormalizeScopePath(file.Path); path != "" {
			paths = append(paths, path)
		}
	}

	return paths
}

func RequiredAcceptanceEvidenceIDsFromPrompt(prompt string) []string {
	if atom, ok := AtomExecutionContractFromPrompt(prompt); ok {
		if ids := requiredAcceptanceEvidenceIDsFromAtom(atom); len(ids) > 0 {
			return ids
		}
	}

	contract, ok := PlanExecutionContractFromPrompt(prompt)
	if !ok {
		return nil
	}

	atoms := contract.Normalized().Atoms
	if len(atoms) != 1 {
		return nil
	}

	return requiredAcceptanceEvidenceIDsFromAtom(atoms[0])
}

func requiredAcceptanceEvidenceIDsFromAtom(atom AtomExecutionContract) []string {
	var ids []string
	for _, obligation := range atom.Normalized().AcceptanceObligations {
		if !obligation.Required || obligation.ID == "" {
			continue
		}

		ids = append(ids, obligation.ID)
	}

	return normalizePromptStableIDs(ids)
}

func decodePromptJSONAfterMarker(prompt string, marker string, into any) bool {
	idx := strings.Index(prompt, marker)
	if idx < 0 {
		return false
	}

	start := strings.Index(prompt[idx+len(marker):], "{")
	if start < 0 {
		return false
	}
	start += idx + len(marker)

	decoder := json.NewDecoder(strings.NewReader(prompt[start:]))
	return decoder.Decode(into) == nil
}

func normalizePromptStableIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = NormalizeStableContractIDPart(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	sort.Strings(out)
	return out
}
