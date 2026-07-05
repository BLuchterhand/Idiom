package intentloop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

type IntentLoopContract struct {
	AtomID                string                                    `json:"atom_id,omitempty"`
	AtomTitle             string                                    `json:"atom_title,omitempty"`
	RequireSourceSanity   bool                                      `json:"require_source_sanity,omitempty"`
	RequireVerification   bool                                      `json:"require_verification,omitempty"`
	RequireReview         bool                                      `json:"require_review,omitempty"`
	RequireRequiredTests  bool                                      `json:"require_required_tests,omitempty"`
	RequiredTestCommands  []plancontract.RequiredTest               `json:"required_test_commands,omitempty"`
	AcceptanceObligations []plancontract.AcceptanceEvidenceRequired `json:"acceptance_obligations,omitempty"`
	ScopeContract         plancontract.PlanScopeContract            `json:"scope_contract"`
	AuthorizedNewFiles    []plancontract.AuthorizedNewFile          `json:"authorized_new_files,omitempty"`
	AuthorizedNewRoots    []plancontract.AuthorizedNewRoot          `json:"authorized_new_roots,omitempty"`
	AuthorityDeltas       []plancontract.AuthorityDelta             `json:"authority_deltas,omitempty"`
	ContractHash          string                                    `json:"contract_hash,omitempty"`
}

type ContractExtractionInput struct {
	OriginalPrompt        string
	RequiredCoveragePaths []string
	AllowedCandidatePaths []string
	WriteAllowedPaths     []string
}

func ExtractContract(input ContractExtractionInput) (IntentLoopContract, error) {
	wireContract, err := extractWireContract(input.OriginalPrompt)
	if err != nil {
		return IntentLoopContract{}, err
	}

	contract := IntentLoopContract{}
	if len(wireContract.Atoms) > 0 {
		atom := wireContract.Atoms[0]
		contract.AtomID = strings.TrimSpace(atom.AtomID)
		contract.AtomTitle = strings.TrimSpace(atom.AtomTitle)
		contract.RequireSourceSanity = atom.TerminalEvidence.RequireSourceSanity
		contract.RequireVerification = atom.TerminalEvidence.RequireVerification
		contract.RequireReview = atom.TerminalEvidence.RequireReview
		contract.RequireRequiredTests = atom.TerminalEvidence.RequireRequiredTests
		contract.RequiredTestCommands = normalizeRequiredTests(atom.RequiredTests)
		contract.AcceptanceObligations = normalizeAcceptanceObligations(atom.AcceptanceObligations)
		contract.ScopeContract = atom.Scope.Normalized()
		contract.AuthorizedNewFiles = append([]plancontract.AuthorizedNewFile(nil), atom.AuthorizedNewFiles...)
		contract.AuthorizedNewRoots = append([]plancontract.AuthorizedNewRoot(nil), atom.AuthorizedNewRoots...)
		contract.AuthorityDeltas = append([]plancontract.AuthorityDelta(nil), atom.AuthorityDeltas...)
	}

	contract.ScopeContract = FreezeScopeContract(contract.ScopeContract, input)
	contract.ContractHash = stableJSONHash(contract)
	return contract, nil
}

func FreezeScopeContract(base plancontract.PlanScopeContract, input ContractExtractionInput) plancontract.PlanScopeContract {
	required := append([]string(nil), base.RequiredCoveragePaths...)
	required = append(required, input.RequiredCoveragePaths...)

	allowed := append([]string(nil), base.AllowedCandidatePaths...)
	allowed = append(allowed, input.AllowedCandidatePaths...)

	writeAllowed := append([]string(nil), base.WriteAllowedPaths...)
	writeAllowed = append(writeAllowed, input.WriteAllowedPaths...)

	base.RequiredCoveragePaths = required
	base.AllowedCandidatePaths = allowed
	if len(input.WriteAllowedPaths) > 0 {
		base.WriteAllowedPaths = writeAllowed
		base.WriteAllowedExplicit = true
	}

	return base.Normalized()
}

func (contract IntentLoopContract) RequiredTestCommandStrings() []string {
	var commands []string
	for _, test := range contract.RequiredTestCommands {
		command := strings.TrimSpace(test.Command)
		if command != "" {
			commands = append(commands, command)
		}
	}
	return uniqueStrings(commands)
}

type wirePlanExecutionContract struct {
	Atoms              []wireAtomExecutionContract      `json:"atoms,omitempty"`
	AuthorizedNewFiles []plancontract.AuthorizedNewFile `json:"authorized_new_files,omitempty"`
	AuthorizedNewRoots []plancontract.AuthorizedNewRoot `json:"authorized_new_roots,omitempty"`
	AuthorityDeltas    []plancontract.AuthorityDelta    `json:"authority_deltas,omitempty"`
}

type wireAtomExecutionContract struct {
	AtomID                string                                    `json:"atom_id,omitempty"`
	AtomTitle             string                                    `json:"atom_title,omitempty"`
	Scope                 plancontract.PlanScopeContract            `json:"scope"`
	RequiredTests         []plancontract.RequiredTest               `json:"required_tests,omitempty"`
	AcceptanceObligations []plancontract.AcceptanceEvidenceRequired `json:"acceptance_obligations,omitempty"`
	TerminalEvidence      wireTerminalEvidenceRequirements          `json:"terminal_evidence_requirements,omitempty"`
	AuthorizedNewFiles    []plancontract.AuthorizedNewFile          `json:"authorized_new_files,omitempty"`
	AuthorizedNewRoots    []plancontract.AuthorizedNewRoot          `json:"authorized_new_roots,omitempty"`
	AuthorityDeltas       []plancontract.AuthorityDelta             `json:"authority_deltas,omitempty"`
}

type wireTerminalEvidenceRequirements struct {
	RequireSourceSanity  bool `json:"require_source_sanity,omitempty"`
	RequireVerification  bool `json:"require_verification,omitempty"`
	RequireReview        bool `json:"require_review,omitempty"`
	RequireRequiredTests bool `json:"require_required_tests,omitempty"`
}

func extractWireContract(prompt string) (wirePlanExecutionContract, error) {
	body := jsonAfterPromptMarker(prompt, plancontract.PromptPlanExecutionContractMarker)
	if body == "" {
		return wirePlanExecutionContract{}, nil
	}

	var contract wirePlanExecutionContract
	if err := json.Unmarshal([]byte(body), &contract); err != nil {
		return wirePlanExecutionContract{}, fmt.Errorf("decode typed plan execution contract: %w", err)
	}

	for i := range contract.Atoms {
		contract.Atoms[i].AuthorizedNewFiles = append(contract.AuthorizedNewFiles, contract.Atoms[i].AuthorizedNewFiles...)
		contract.Atoms[i].AuthorizedNewRoots = append(contract.AuthorizedNewRoots, contract.Atoms[i].AuthorizedNewRoots...)
		contract.Atoms[i].AuthorityDeltas = append(contract.AuthorityDeltas, contract.Atoms[i].AuthorityDeltas...)
	}

	return contract, nil
}

func jsonAfterPromptMarker(prompt string, marker string) string {
	idx := strings.Index(prompt, marker)
	if idx < 0 {
		return ""
	}

	start := strings.Index(prompt[idx+len(marker):], "{")
	if start < 0 {
		return ""
	}
	start += idx + len(marker)

	end := jsonObjectEnd(prompt, start)
	if end <= start {
		return ""
	}

	return strings.TrimSpace(prompt[start:end])
}

func jsonObjectEnd(value string, start int) int {
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

func normalizeRequiredTests(tests []plancontract.RequiredTest) []plancontract.RequiredTest {
	out := make([]plancontract.RequiredTest, 0, len(tests))
	seen := map[string]struct{}{}
	for _, test := range tests {
		test.ID = strings.TrimSpace(test.ID)
		test.Command = strings.TrimSpace(test.Command)
		test.Source = strings.TrimSpace(test.Source)
		if test.Command == "" {
			continue
		}
		key := test.ID + "\x00" + test.Command
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, test)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return out[i].Command < out[j].Command
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizeAcceptanceObligations(obligations []plancontract.AcceptanceEvidenceRequired) []plancontract.AcceptanceEvidenceRequired {
	out := make([]plancontract.AcceptanceEvidenceRequired, 0, len(obligations))
	seen := map[string]struct{}{}
	for _, obligation := range obligations {
		obligation.ID = strings.TrimSpace(obligation.ID)
		obligation.Description = strings.TrimSpace(obligation.Description)
		obligation.Source = strings.TrimSpace(obligation.Source)
		obligation.Threshold = strings.TrimSpace(obligation.Threshold)
		if obligation.ID == "" {
			continue
		}
		if _, ok := seen[obligation.ID]; ok {
			continue
		}
		seen[obligation.ID] = struct{}{}
		out = append(out, obligation)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
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

func stableJSONHash(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return sha256Hex(string(body))
}
