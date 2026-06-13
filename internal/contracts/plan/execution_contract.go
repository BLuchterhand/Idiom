package plancontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const PlanExecutionContractRevision = "plan_execution_contract.v1"

type PlanExecutionContract struct {
	Revision                  string                     `json:"revision"`
	ContractID                string                     `json:"contract_id,omitempty"`
	ContractHash              string                     `json:"contract_hash,omitempty"`
	PlanID                    string                     `json:"plan_id,omitempty"`
	Atoms                     []AtomExecutionContract    `json:"atoms,omitempty"`
	AuthorizedNewFiles        []AuthorizedNewFile        `json:"authorized_new_files,omitempty"`
	AuthorizedNewRoots        []AuthorizedNewRoot        `json:"authorized_new_roots,omitempty"`
	AuthorityDeltas           []AuthorityDelta           `json:"authority_deltas,omitempty"`
	RecoveryInvariantFailures []RecoveryInvariantFailure `json:"recovery_invariant_failures,omitempty"`
}

type AtomExecutionContract struct {
	AtomID                    string                       `json:"atom_id,omitempty"`
	AtomTitle                 string                       `json:"atom_title,omitempty"`
	Scope                     PlanScopeContract            `json:"scope"`
	RequiredTests             []RequiredTest               `json:"required_tests,omitempty"`
	TDDObligations            []TDDObligation              `json:"tdd_obligations,omitempty"`
	AcceptanceObligations     []AcceptanceEvidenceRequired `json:"acceptance_obligations,omitempty"`
	SourceEvidenceObligations []SourceEvidenceObligation   `json:"source_evidence_obligations,omitempty"`
	TerminalEvidence          TerminalEvidenceRequirements `json:"terminal_evidence_requirements,omitempty"`
	SourceSatisfied           SourceSatisfiedRequirements  `json:"source_satisfied_requirements,omitempty"`
	AuthorizedNewFiles        []AuthorizedNewFile          `json:"authorized_new_files,omitempty"`
	AuthorizedNewRoots        []AuthorizedNewRoot          `json:"authorized_new_roots,omitempty"`
	AuthorityDeltas           []AuthorityDelta             `json:"authority_deltas,omitempty"`
	RecoveryInvariantFailures []RecoveryInvariantFailure   `json:"recovery_invariant_failures,omitempty"`
}

type RequiredTest struct {
	ID      string `json:"id,omitempty"`
	Command string `json:"command"`
	Source  string `json:"source,omitempty"`
}

type TDDObligation struct {
	ID                string   `json:"id,omitempty"`
	Command           string   `json:"command"`
	Behavior          string   `json:"behavior,omitempty"`
	TestArtifactPaths []string `json:"test_artifact_paths,omitempty"`
	Source            string   `json:"source,omitempty"`
}

type AcceptanceEvidenceRequired struct {
	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Source      string `json:"source,omitempty"`
}

type SourceEvidenceObligation struct {
	ID       string `json:"id,omitempty"`
	Path     string `json:"path,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Required bool   `json:"required,omitempty"`
	Source   string `json:"source,omitempty"`
}

type TerminalEvidenceRequirements struct {
	Explicit                    bool `json:"explicit,omitempty"`
	RequireSourceEvidence       bool `json:"require_source_evidence,omitempty"`
	RequireChangedPathCoverage  bool `json:"require_changed_path_coverage,omitempty"`
	RequireRequiredPathCoverage bool `json:"require_required_path_coverage,omitempty"`
	RequireSourceSanity         bool `json:"require_source_sanity,omitempty"`
	RequireVerification         bool `json:"require_verification,omitempty"`
	RequireReview               bool `json:"require_review,omitempty"`
	RequireRequiredTests        bool `json:"require_required_tests,omitempty"`
	RequireTestArtifacts        bool `json:"require_test_artifacts,omitempty"`
}

type SourceSatisfiedRequirements struct {
	Explicit                    bool `json:"explicit,omitempty"`
	Allowed                     bool `json:"allowed,omitempty"`
	RequireCurrentStateEvidence bool `json:"require_current_state_evidence,omitempty"`
	RequireRequiredScope        bool `json:"require_required_scope,omitempty"`
	RequireNoChangedPaths       bool `json:"require_no_changed_paths,omitempty"`
}

type AuthorizedNewFile struct {
	Path     string                       `json:"path"`
	Reason   string                       `json:"reason,omitempty"`
	Source   string                       `json:"source,omitempty"`
	Metadata NewFileAuthorizationMetadata `json:"metadata,omitempty"`
}

type AuthorizedNewRoot struct {
	Root     string                       `json:"root"`
	Reason   string                       `json:"reason,omitempty"`
	Source   string                       `json:"source,omitempty"`
	Metadata NewFileAuthorizationMetadata `json:"metadata,omitempty"`
}

type NewFileAuthorizationMetadata struct {
	Compiler  string `json:"compiler,omitempty"`
	Rule      string `json:"rule,omitempty"`
	InputHash string `json:"input_hash,omitempty"`
}

type AuthorityDeltaKind string

const (
	AuthorityDeltaKindAddNewFile          AuthorityDeltaKind = "add_new_file"
	AuthorityDeltaKindAddNewRoot          AuthorityDeltaKind = "add_new_root"
	AuthorityDeltaKindPromoteRequiredPath AuthorityDeltaKind = "promote_required_path"
	AuthorityDeltaKindDemoteRequiredPath  AuthorityDeltaKind = "demote_required_path"
	AuthorityDeltaKindNarrowWriteEnvelope AuthorityDeltaKind = "narrow_write_envelope"
	AuthorityDeltaKindExpandWriteEnvelope AuthorityDeltaKind = "expand_write_envelope"
)

type AuthorityDelta struct {
	Kind        AuthorityDeltaKind    `json:"kind"`
	Path        string                `json:"path,omitempty"`
	From        string                `json:"from,omitempty"`
	To          string                `json:"to,omitempty"`
	Reason      string                `json:"reason,omitempty"`
	Source      string                `json:"source,omitempty"`
	Approved    bool                  `json:"approved,omitempty"`
	SourceFacts []SourceAuthorityFact `json:"source_facts,omitempty"`
}

type SourceAuthorityFact struct {
	Path      string `json:"path,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	Signature string `json:"signature,omitempty"`
	Anchor    string `json:"anchor,omitempty"`
}

type RecoveryInvariantFailure struct {
	Code      string   `json:"code"`
	Message   string   `json:"message,omitempty"`
	Phase     string   `json:"phase,omitempty"`
	AtomID    string   `json:"atom_id,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
	Recovered bool     `json:"recovered,omitempty"`
}

type TerminalEvidenceInput struct {
	AtomID                    string                       `json:"atom_id,omitempty"`
	TerminalStatus            string                       `json:"terminal_status,omitempty"`
	ChangedPaths              []string                     `json:"changed_paths,omitempty"`
	RequiredPathCoveragePaths []string                     `json:"required_path_coverage_paths,omitempty"`
	SourceEvidence            []SourceSatisfiedEvidenceRef `json:"source_evidence,omitempty"`
	SourceSatisfied           bool                         `json:"source_satisfied,omitempty"`
	SourceSatisfiedPaths      []string                     `json:"source_satisfied_paths,omitempty"`
	SourceSanityPassed        bool                         `json:"source_sanity_passed,omitempty"`
	VerificationPassed        bool                         `json:"verification_passed,omitempty"`
	ReviewPassed              bool                         `json:"review_passed,omitempty"`
	RequiredTestResults       []RequiredTestEvidence       `json:"required_test_results,omitempty"`
	TDDCommandResults         []TDDCommandEvidence         `json:"tdd_command_results,omitempty"`
	TestArtifacts             []string                     `json:"test_artifacts,omitempty"`
	AcceptanceEvidenceIDs     []string                     `json:"acceptance_evidence_ids,omitempty"`
}

type RequiredTestEvidence struct {
	ID      string `json:"id,omitempty"`
	Command string `json:"command"`
	Passed  bool   `json:"passed"`
}

type TDDCommandEvidence struct {
	ID            string   `json:"id,omitempty"`
	Command       string   `json:"command"`
	Passed        bool     `json:"passed"`
	ArtifactPaths []string `json:"artifact_paths,omitempty"`
}

type PlanExecutionContractViolation struct {
	Code         string `json:"code"`
	Message      string `json:"message,omitempty"`
	AtomID       string `json:"atom_id,omitempty"`
	Path         string `json:"path,omitempty"`
	ObligationID string `json:"obligation_id,omitempty"`
}

func StrictTerminalEvidenceRequirements() TerminalEvidenceRequirements {
	return TerminalEvidenceRequirements{
		Explicit:                    true,
		RequireSourceEvidence:       true,
		RequireChangedPathCoverage:  true,
		RequireRequiredPathCoverage: true,
		RequireSourceSanity:         true,
		RequireVerification:         true,
		RequireReview:               true,
		RequireRequiredTests:        true,
		RequireTestArtifacts:        true,
	}
}

func (contract PlanExecutionContract) Normalized() PlanExecutionContract {
	contract.Revision = strings.TrimSpace(contract.Revision)
	if contract.Revision == "" {
		contract.Revision = PlanExecutionContractRevision
	}

	contract.PlanID = NormalizeStableContractIDPart(contract.PlanID)
	contract.AuthorizedNewFiles = normalizeAuthorizedNewFiles(contract.AuthorizedNewFiles)
	contract.AuthorizedNewRoots = normalizeAuthorizedNewRoots(contract.AuthorizedNewRoots)
	contract.AuthorityDeltas = normalizeAuthorityDeltas(contract.AuthorityDeltas)
	contract.RecoveryInvariantFailures = normalizeRecoveryInvariantFailures(contract.RecoveryInvariantFailures)

	for i := range contract.Atoms {
		contract.Atoms[i] = contract.Atoms[i].normalized(contract.PlanID)
	}

	sort.SliceStable(contract.Atoms, func(i, j int) bool {
		return contract.Atoms[i].AtomID < contract.Atoms[j].AtomID
	})

	contract.ContractID = ""
	contract.ContractHash = StablePlanExecutionContractHash(contract)
	contract.ContractID = StablePlanExecutionContractID(contract)

	return contract
}

func (atom AtomExecutionContract) Normalized() AtomExecutionContract {
	return atom.normalized("")
}

func (atom AtomExecutionContract) normalized(planID string) AtomExecutionContract {
	atom.AtomID = NormalizeStableContractIDPart(atom.AtomID)
	atom.AtomTitle = strings.TrimSpace(atom.AtomTitle)
	atom.Scope = normalizeExecutionScope(atom.Scope)
	atom.RequiredTests = normalizeRequiredTests(atom.RequiredTests)
	atom.TDDObligations = normalizeTDDObligations(atom.TDDObligations)
	atom.AcceptanceObligations = normalizeAcceptanceObligations(atom.AcceptanceObligations)
	atom.SourceEvidenceObligations = normalizeSourceEvidenceObligations(atom.SourceEvidenceObligations)
	atom.TerminalEvidence = atom.TerminalEvidence.normalized(atom)
	atom.SourceSatisfied = atom.SourceSatisfied.normalized()
	atom.AuthorizedNewFiles = normalizeAuthorizedNewFiles(atom.AuthorizedNewFiles)
	atom.AuthorizedNewRoots = normalizeAuthorizedNewRoots(atom.AuthorizedNewRoots)
	atom.AuthorityDeltas = normalizeAuthorityDeltas(atom.AuthorityDeltas)
	atom.RecoveryInvariantFailures = normalizeRecoveryInvariantFailures(atom.RecoveryInvariantFailures)

	if atom.AtomID == "" {
		seed := atom

		seed.AtomID = ""
		atom.AtomID = StableAtomExecutionContractID(planID, seed)
	}

	return atom
}

func (atom AtomExecutionContract) RequiredEditPaths() []string {
	return atom.Normalized().Scope.RequiredEditPaths()
}

func (atom AtomExecutionContract) AllowedEditPaths() []string {
	return atom.Normalized().Scope.AllowedEditPaths()
}

func (atom AtomExecutionContract) WriteEnvelopePaths() []string {
	return atom.Normalized().Scope.WriteEnvelopePaths()
}

func StablePlanExecutionContractHash(contract PlanExecutionContract) string {
	canonical := contract

	canonical.ContractID = ""
	canonical.ContractHash = ""

	for i := range canonical.Atoms {
		canonical.Atoms[i].AtomID = NormalizeStableContractIDPart(canonical.Atoms[i].AtomID)
	}

	body, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}

	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

func StablePlanExecutionContractID(contract PlanExecutionContract) string {
	hash := StablePlanExecutionContractHash(contract)
	if len(hash) < 16 {
		return ""
	}

	return "planexec_" + hash[:16]
}

func StableAtomExecutionContractID(planID string, atom AtomExecutionContract) string {
	atom.AtomID = ""

	body, err := json.Marshal(struct {
		Revision string                `json:"revision"`
		PlanID   string                `json:"plan_id,omitempty"`
		Atom     AtomExecutionContract `json:"atom"`
	}{
		Revision: PlanExecutionContractRevision,
		PlanID:   NormalizeStableContractIDPart(planID),
		Atom:     atom,
	})
	if err != nil {
		return ""
	}

	sum := sha256.Sum256(body)

	return "atomexec_" + hex.EncodeToString(sum[:])[:16]
}

func NormalizeStableContractIDPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}

	var b strings.Builder

	lastSep := false

	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)

			lastSep = false

			continue
		}

		if r == '_' || r == '-' || r == '.' {
			if !lastSep {
				b.WriteRune(r)

				lastSep = true
			}

			continue
		}

		if !lastSep {
			b.WriteByte('-')

			lastSep = true
		}
	}

	return strings.Trim(b.String(), "-_.")
}

func ValidatePlanExecutionContractInput(contract PlanExecutionContract) []PlanExecutionContractViolation {
	var violations []PlanExecutionContractViolation

	if strings.TrimSpace(contract.Revision) != "" && strings.TrimSpace(contract.Revision) != PlanExecutionContractRevision {
		violations = append(violations, contractViolation("unsupported_contract_revision", "", "", "", fmt.Sprintf("unsupported plan execution contract revision %q", contract.Revision)))
	}

	if len(contract.Atoms) == 0 {
		violations = append(violations, contractViolation("missing_atoms", "", "", "", "plan execution contract must contain at least one atom"))
	}

	for _, file := range contract.AuthorizedNewFiles {
		if NormalizeScopePath(file.Path) == "" {
			violations = append(violations, contractViolation("invalid_authorized_new_file", "", file.Path, "", "authorized new file path must be repo-relative"))
		}
	}

	for _, root := range contract.AuthorizedNewRoots {
		if normalizeAuthorizedRoot(root.Root) == "" {
			violations = append(violations, contractViolation("invalid_authorized_new_root", "", root.Root, "", "authorized new root must be repo-relative"))
		}
	}

	for _, delta := range contract.AuthorityDeltas {
		violations = append(violations, validateAuthorityDelta("", delta)...)
	}

	for _, atom := range contract.Atoms {
		violations = append(violations, ValidateAtomExecutionContractInput(atom)...)
	}

	return violations
}

func ValidateAtomExecutionContractInput(atom AtomExecutionContract) []PlanExecutionContractViolation {
	var violations []PlanExecutionContractViolation

	rawRequired := atom.Scope.RequiredCoveragePaths
	rawAllowed := atom.Scope.AllowedCandidatePaths
	rawAtomID := NormalizeStableContractIDPart(atom.AtomID)

	for _, path := range rawRequired {
		if strings.TrimSpace(path) != "" && NormalizeScopePath(path) == "" {
			violations = append(violations, contractViolation("invalid_required_edit_path", rawAtomID, path, "", "required edit path must be repo-relative"))
		}
	}

	for _, path := range rawAllowed {
		if strings.TrimSpace(path) != "" && NormalizeScopePath(path) == "" {
			violations = append(violations, contractViolation("invalid_allowed_edit_path", rawAtomID, path, "", "allowed edit path must be repo-relative"))
		}
	}

	for _, path := range atom.Scope.WriteAllowedPaths {
		if strings.TrimSpace(path) != "" && NormalizeScopePath(strings.TrimSuffix(strings.TrimSpace(path), "/**")) == "" {
			violations = append(violations, contractViolation("invalid_write_envelope_path", rawAtomID, path, "", "write envelope path must be repo-relative"))
		}
	}

	for _, file := range atom.AuthorizedNewFiles {
		if strings.TrimSpace(file.Path) != "" && NormalizeScopePath(file.Path) == "" {
			violations = append(violations, contractViolation("invalid_authorized_new_file", rawAtomID, file.Path, "", "authorized new file path must be repo-relative"))
		}
	}

	for _, root := range atom.AuthorizedNewRoots {
		if strings.TrimSpace(root.Root) != "" && normalizeAuthorizedRoot(root.Root) == "" {
			violations = append(violations, contractViolation("invalid_authorized_new_root", rawAtomID, root.Root, "", "authorized new root must be repo-relative"))
		}
	}

	for _, obligation := range atom.TDDObligations {
		for _, path := range obligation.TestArtifactPaths {
			if strings.TrimSpace(path) != "" && NormalizeScopePath(path) == "" {
				violations = append(violations, contractViolation("invalid_tdd_test_artifact_path", rawAtomID, path, obligation.ID, "TDD test artifact path must be repo-relative"))
			}
		}
	}

	for _, obligation := range atom.SourceEvidenceObligations {
		if obligation.Required && strings.TrimSpace(obligation.Path) != "" && NormalizeScopePath(obligation.Path) == "" {
			violations = append(violations, contractViolation("invalid_source_evidence_path", rawAtomID, obligation.Path, obligation.ID, "required source evidence path must be repo-relative"))
		}
	}

	atom = atom.Normalized()
	if !atom.Scope.HasContent() {
		violations = append(violations, contractViolation("missing_scope", atom.AtomID, "", "", "atom execution contract must declare scope"))
	}

	for _, path := range UniqueScopePaths(rawRequired) {
		if containsScopePath(rawAllowed, path) {
			violations = append(violations, contractViolation("required_path_demoted_to_allowed", atom.AtomID, path, "", "required edit paths must not be repeated as allowed edit paths"))
		}
	}

	if atom.Scope.WriteAllowedExplicit {
		for _, path := range atom.Scope.RequiredCoveragePaths {
			if !pathAllowedByPatterns(path, atom.Scope.WriteAllowedPaths) {
				violations = append(violations, contractViolation("required_path_outside_write_envelope", atom.AtomID, path, "", "required edit path is outside the write envelope"))
			}
		}
	}

	for _, test := range atom.RequiredTests {
		if strings.TrimSpace(test.Command) == "" {
			violations = append(violations, contractViolation("missing_required_test_command", atom.AtomID, "", test.ID, "required test must declare a command"))
		}
	}

	for _, obligation := range atom.TDDObligations {
		if strings.TrimSpace(obligation.Command) == "" {
			violations = append(violations, contractViolation("missing_tdd_command", atom.AtomID, "", obligation.ID, "TDD obligation must declare the required command"))
		}
	}

	for _, delta := range atom.AuthorityDeltas {
		violations = append(violations, validateAuthorityDelta(atom.AtomID, delta)...)
	}

	return violations
}

func ValidatePlanTerminalEvidenceInput(contract PlanExecutionContract, evidence TerminalEvidenceInput) []PlanExecutionContractViolation {
	contract = contract.Normalized()

	var violations []PlanExecutionContractViolation

	for _, atom := range contract.Atoms {
		if evidence.AtomID != "" && NormalizeStableContractIDPart(evidence.AtomID) != atom.AtomID {
			continue
		}

		atom.AuthorizedNewFiles = append(append([]AuthorizedNewFile(nil), contract.AuthorizedNewFiles...), atom.AuthorizedNewFiles...)
		atom.AuthorizedNewRoots = append(append([]AuthorizedNewRoot(nil), contract.AuthorizedNewRoots...), atom.AuthorizedNewRoots...)
		violations = append(violations, ValidateAtomTerminalEvidenceInput(atom, evidence)...)
	}

	if evidence.AtomID != "" && len(violations) == 0 && !contractHasAtom(contract, evidence.AtomID) {
		violations = append(violations, contractViolation("unknown_terminal_evidence_atom", NormalizeStableContractIDPart(evidence.AtomID), "", "", "terminal evidence references an atom outside the plan execution contract"))
	}

	return violations
}

func ValidateAtomTerminalEvidenceInput(atom AtomExecutionContract, evidence TerminalEvidenceInput) []PlanExecutionContractViolation {
	atom = atom.Normalized()
	if evidence.AtomID != "" && NormalizeStableContractIDPart(evidence.AtomID) != atom.AtomID {
		return nil
	}

	if !terminalStatusIsSuccess(evidence.TerminalStatus) {
		return nil
	}

	requirements := atom.TerminalEvidence.normalized(atom)

	var violations []PlanExecutionContractViolation

	changedPaths := UniqueScopePaths(evidence.ChangedPaths)

	if len(changedPaths) == 0 &&
		len(evidence.SourceEvidence) == 0 &&
		len(evidence.RequiredTestResults) == 0 &&
		len(evidence.TDDCommandResults) == 0 &&
		len(evidence.TestArtifacts) == 0 &&
		!evidence.SourceSatisfied {

		violations = append(violations, contractViolation("terminal_success_missing_evidence", atom.AtomID, "", "", "terminal success requires structured evidence"))
	}

	if requirements.RequireChangedPathCoverage && len(changedPaths) == 0 && !evidence.SourceSatisfied {
		violations = append(violations, contractViolation("changed_path_coverage_missing", atom.AtomID, "", "", "terminal success lacks changed-path coverage"))
	}

	for _, path := range changedPaths {
		if !atom.pathAuthorizedForWrite(path) {
			violations = append(violations, contractViolation("changed_path_outside_write_envelope", atom.AtomID, path, "", "changed path is outside the typed write envelope"))
		}
	}

	requiredCoverage := terminalRequiredPathCoverage(atom, evidence)
	if requirements.RequireRequiredPathCoverage {
		for _, path := range atom.Scope.RequiredCoveragePaths {
			if _, ok := requiredCoverage[path]; !ok {
				violations = append(violations, contractViolation("required_path_coverage_missing", atom.AtomID, path, "", "terminal success lacks changed-path or source-satisfied coverage for a required edit path"))
			}
		}
	}

	if requirements.RequireSourceEvidence {
		violations = append(violations, validateTerminalSourceEvidence(atom, evidence)...)
	}

	if requirements.RequireSourceSanity && !evidence.SourceSanityPassed {
		violations = append(violations, contractViolation("source_sanity_failed_or_missing", atom.AtomID, "", "", "terminal success requires passing source sanity evidence"))
	}

	if requirements.RequireVerification && !evidence.VerificationPassed {
		violations = append(violations, contractViolation("verification_failed_or_missing", atom.AtomID, "", "", "terminal success requires passing verification evidence"))
	}

	if requirements.RequireReview && !evidence.ReviewPassed {
		violations = append(violations, contractViolation("review_failed_or_missing", atom.AtomID, "", "", "terminal success requires passing review evidence"))
	}

	if requirements.RequireRequiredTests {
		violations = append(violations, validateRequiredTestEvidence(atom, evidence)...)
	}

	if requirements.RequireTestArtifacts {
		violations = append(violations, validateTDDObligationEvidence(atom, evidence)...)
	}

	violations = append(violations, validateAcceptanceEvidence(atom, evidence)...)
	violations = append(violations, validateSourceSatisfiedEvidence(atom, evidence)...)

	return violations
}

func (requirements TerminalEvidenceRequirements) normalized(atom AtomExecutionContract) TerminalEvidenceRequirements {
	if requirements.Explicit {
		return requirements
	}

	requirements.RequireSourceEvidence = len(atom.Scope.RequiredCoveragePaths) > 0 || len(atom.SourceEvidenceObligations) > 0
	requirements.RequireChangedPathCoverage = true
	requirements.RequireRequiredPathCoverage = len(atom.Scope.RequiredCoveragePaths) > 0
	requirements.RequireSourceSanity = true
	requirements.RequireVerification = true
	requirements.RequireReview = true
	requirements.RequireRequiredTests = len(atom.RequiredTests) > 0 || len(atom.TDDObligations) > 0
	requirements.RequireTestArtifacts = atomHasRequiredTestArtifacts(atom)

	return requirements
}

func (requirements SourceSatisfiedRequirements) normalized() SourceSatisfiedRequirements {
	if requirements.Explicit {
		return requirements
	}

	requirements.Allowed = true
	requirements.RequireCurrentStateEvidence = true
	requirements.RequireRequiredScope = true
	requirements.RequireNoChangedPaths = true

	return requirements
}

func normalizeExecutionScope(scope PlanScopeContract) PlanScopeContract {
	scope.RequiredCoveragePaths = uniqueSortedScopePaths(scope.RequiredCoveragePaths)
	scope.AllowedCandidatePaths = uniqueSortedScopePaths(scope.AllowedCandidatePaths)
	scope.AllowedCandidatePaths = scopePathsExcludingRequired(scope.AllowedCandidatePaths, scope.RequiredCoveragePaths)
	scope.WriteAllowedPaths = uniqueSortedScopePaths(scope.WriteAllowedPaths)
	scope.EditSetObligations = scope.EditSetObligations.Normalized()
	sort.SliceStable(scope.EditSetObligations, func(i, j int) bool {
		if scope.EditSetObligations[i].Kind == scope.EditSetObligations[j].Kind {
			return scope.EditSetObligations[i].Source < scope.EditSetObligations[j].Source
		}

		return scope.EditSetObligations[i].Kind < scope.EditSetObligations[j].Kind
	})

	scope.ContractID = ""
	if scope.HasContent() {
		scope.ContractID = planScopeContractID(scope)
	}

	return scope
}

func normalizeRequiredTests(tests []RequiredTest) []RequiredTest {
	out := make([]RequiredTest, 0, len(tests))
	seen := map[string]struct{}{}

	for _, test := range tests {
		test.ID = NormalizeStableContractIDPart(test.ID)
		test.Command = normalizeCommand(test.Command)
		test.Source = strings.TrimSpace(test.Source)

		key := test.ID + "\x00" + test.Command

		if test.Command == "" {
			key = test.ID
		}

		if key == "" {
			continue
		}

		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, test)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID+"\x00"+out[i].Command < out[j].ID+"\x00"+out[j].Command
	})

	return out
}

func normalizeTDDObligations(obligations []TDDObligation) []TDDObligation {
	out := make([]TDDObligation, 0, len(obligations))
	seen := map[string]struct{}{}

	for _, obligation := range obligations {
		obligation.ID = NormalizeStableContractIDPart(obligation.ID)
		obligation.Command = normalizeCommand(obligation.Command)
		obligation.Behavior = strings.TrimSpace(obligation.Behavior)
		obligation.Source = strings.TrimSpace(obligation.Source)
		obligation.TestArtifactPaths = uniqueSortedScopePaths(obligation.TestArtifactPaths)

		if obligation.ID == "" && obligation.Command == "" && obligation.Behavior == "" {
			continue
		}

		key := obligation.ID + "\x00" + obligation.Command + "\x00" + obligation.Behavior
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, obligation)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID+"\x00"+out[i].Command < out[j].ID+"\x00"+out[j].Command
	})

	return out
}

func normalizeAcceptanceObligations(obligations []AcceptanceEvidenceRequired) []AcceptanceEvidenceRequired {
	out := make([]AcceptanceEvidenceRequired, 0, len(obligations))
	seen := map[string]struct{}{}

	for _, obligation := range obligations {
		obligation.ID = NormalizeStableContractIDPart(obligation.ID)
		obligation.Description = strings.TrimSpace(obligation.Description)
		obligation.Source = strings.TrimSpace(obligation.Source)

		if obligation.ID == "" {
			obligation.ID = stableAcceptanceEvidenceID(obligation)
		}

		if obligation.ID == "" && obligation.Description == "" {
			continue
		}

		if !obligation.Required {
			obligation.Required = true
		}

		key := obligation.ID + "\x00" + obligation.Description
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, obligation)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID+"\x00"+out[i].Description < out[j].ID+"\x00"+out[j].Description
	})

	return out
}

func stableAcceptanceEvidenceID(obligation AcceptanceEvidenceRequired) string {
	seed := strings.TrimSpace(obligation.Description)
	if seed == "" {
		return ""
	}

	if normalized := NormalizeStableContractIDPart(seed); normalized != "" && len(normalized) <= 48 {
		return normalized
	}

	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(obligation.Source),
		seed,
	}, "\x00")))

	return "acceptance-" + hex.EncodeToString(sum[:])[:12]
}

func normalizeSourceEvidenceObligations(obligations []SourceEvidenceObligation) []SourceEvidenceObligation {
	out := make([]SourceEvidenceObligation, 0, len(obligations))
	seen := map[string]struct{}{}

	for _, obligation := range obligations {
		obligation.ID = NormalizeStableContractIDPart(obligation.ID)
		obligation.Path = NormalizeScopePath(obligation.Path)
		obligation.Kind = normalizeEvidenceKind(obligation.Kind)
		obligation.Source = strings.TrimSpace(obligation.Source)

		if obligation.Path == "" && obligation.ID == "" {
			continue
		}

		if !obligation.Required {
			obligation.Required = true
		}

		key := obligation.ID + "\x00" + obligation.Path + "\x00" + obligation.Kind
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, obligation)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID+"\x00"+out[i].Path+"\x00"+out[i].Kind < out[j].ID+"\x00"+out[j].Path+"\x00"+out[j].Kind
	})

	return out
}

func normalizeAuthorizedNewFiles(files []AuthorizedNewFile) []AuthorizedNewFile {
	out := make([]AuthorizedNewFile, 0, len(files))
	seen := map[string]struct{}{}

	for _, file := range files {
		file.Path = NormalizeScopePath(file.Path)
		file.Reason = strings.TrimSpace(file.Reason)
		file.Source = strings.TrimSpace(file.Source)
		file.Metadata = normalizeAuthorizationMetadata(file.Metadata)

		if file.Path == "" {
			continue
		}

		if _, ok := seen[file.Path]; ok {
			continue
		}

		seen[file.Path] = struct{}{}
		out = append(out, file)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })

	return out
}

func normalizeAuthorizedNewRoots(roots []AuthorizedNewRoot) []AuthorizedNewRoot {
	out := make([]AuthorizedNewRoot, 0, len(roots))
	seen := map[string]struct{}{}

	for _, root := range roots {
		root.Root = normalizeAuthorizedRoot(root.Root)
		root.Reason = strings.TrimSpace(root.Reason)
		root.Source = strings.TrimSpace(root.Source)
		root.Metadata = normalizeAuthorizationMetadata(root.Metadata)

		if root.Root == "" {
			continue
		}

		if _, ok := seen[root.Root]; ok {
			continue
		}

		seen[root.Root] = struct{}{}
		out = append(out, root)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Root < out[j].Root })

	return out
}

func normalizeAuthorityDeltas(deltas []AuthorityDelta) []AuthorityDelta {
	out := make([]AuthorityDelta, 0, len(deltas))
	seen := map[string]struct{}{}

	for _, delta := range deltas {
		delta.Path = NormalizeScopePath(delta.Path)
		delta.From = strings.TrimSpace(delta.From)
		delta.To = strings.TrimSpace(delta.To)
		delta.Reason = strings.TrimSpace(delta.Reason)
		delta.Source = strings.TrimSpace(delta.Source)

		if delta.Kind == "" && delta.Path == "" {
			continue
		}

		key := string(delta.Kind) + "\x00" + delta.Path + "\x00" + delta.From + "\x00" + delta.To
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, delta)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return string(out[i].Kind)+"\x00"+out[i].Path < string(out[j].Kind)+"\x00"+out[j].Path
	})

	return out
}

func normalizeRecoveryInvariantFailures(failures []RecoveryInvariantFailure) []RecoveryInvariantFailure {
	out := make([]RecoveryInvariantFailure, 0, len(failures))
	for _, failure := range failures {
		failure.Code = NormalizeStableContractIDPart(failure.Code)
		failure.Message = strings.TrimSpace(failure.Message)
		failure.Phase = NormalizeStableContractIDPart(failure.Phase)
		failure.AtomID = NormalizeStableContractIDPart(failure.AtomID)
		failure.Paths = uniqueSortedScopePaths(failure.Paths)
		failure.Evidence = uniqueSortedStrings(failure.Evidence)

		if failure.Code == "" {
			continue
		}

		out = append(out, failure)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code == out[j].Code {
			return out[i].AtomID < out[j].AtomID
		}

		return out[i].Code < out[j].Code
	})

	return out
}

func normalizeAuthorizationMetadata(metadata NewFileAuthorizationMetadata) NewFileAuthorizationMetadata {
	metadata.Compiler = strings.TrimSpace(metadata.Compiler)
	metadata.Rule = NormalizeStableContractIDPart(metadata.Rule)
	metadata.InputHash = strings.TrimSpace(metadata.InputHash)

	return metadata
}

func validateAuthorityDelta(atomID string, delta AuthorityDelta) []PlanExecutionContractViolation {
	if strings.TrimSpace(delta.Path) != "" && NormalizeScopePath(delta.Path) == "" {
		return []PlanExecutionContractViolation{contractViolation("invalid_authority_delta_path", atomID, delta.Path, "", "authority delta path must be repo-relative")}
	}

	normalized := normalizeAuthorityDeltas([]AuthorityDelta{delta})
	if len(normalized) == 0 {
		return []PlanExecutionContractViolation{contractViolation("invalid_authority_delta", atomID, "", "", "authority delta must declare a kind")}
	}

	delta = normalized[0]
	switch delta.Kind {
	case AuthorityDeltaKindDemoteRequiredPath:
		return []PlanExecutionContractViolation{contractViolation("required_path_demotion_rejected", atomID, delta.Path, "", "required edit path authority cannot be demoted by downstream plan execution")}
	case AuthorityDeltaKindAddNewFile, AuthorityDeltaKindAddNewRoot, AuthorityDeltaKindPromoteRequiredPath, AuthorityDeltaKindNarrowWriteEnvelope, AuthorityDeltaKindExpandWriteEnvelope:
		return nil
	case "":
		return []PlanExecutionContractViolation{contractViolation("invalid_authority_delta", atomID, delta.Path, "", "authority delta must declare a kind")}
	default:
		return []PlanExecutionContractViolation{contractViolation("unknown_authority_delta", atomID, delta.Path, "", fmt.Sprintf("unknown authority delta kind %q", delta.Kind))}
	}
}

func validateTerminalSourceEvidence(atom AtomExecutionContract, evidence TerminalEvidenceInput) []PlanExecutionContractViolation {
	var violations []PlanExecutionContractViolation

	evidenceByPath := sourceSatisfiedCurrentStateEvidenceSet(evidence.SourceEvidence)

	if len(atom.SourceEvidenceObligations) > 0 {
		for _, obligation := range atom.SourceEvidenceObligations {
			if !obligation.Required {
				continue
			}

			if _, ok := evidenceByPath[obligation.Path]; !ok {
				violations = append(violations, contractViolation("required_source_evidence_missing", atom.AtomID, obligation.Path, obligation.ID, "terminal success lacks required source evidence"))
			}
		}

		return violations
	}

	if len(atom.Scope.RequiredCoveragePaths) == 0 {
		return nil
	}

	for _, path := range atom.Scope.RequiredCoveragePaths {
		if _, ok := evidenceByPath[path]; ok {
			return nil
		}
	}

	violations = append(violations, contractViolation("required_source_evidence_missing", atom.AtomID, "", "", "terminal success lacks source evidence for required scope"))

	return violations
}

func validateRequiredTestEvidence(atom AtomExecutionContract, evidence TerminalEvidenceInput) []PlanExecutionContractViolation {
	var violations []PlanExecutionContractViolation

	for _, test := range atom.RequiredTests {
		result, ok := findRequiredTestEvidence(test, evidence.RequiredTestResults)
		if !ok {
			violations = append(violations, contractViolation("required_test_missing", atom.AtomID, "", test.ID, "terminal success lacks required test result"))
			continue
		}

		if normalizeCommand(result.Command) != normalizeCommand(test.Command) {
			violations = append(violations, contractViolation("required_test_command_changed", atom.AtomID, "", test.ID, "terminal evidence changed the required test command"))
		}

		if !result.Passed {
			violations = append(violations, contractViolation("required_test_failed", atom.AtomID, "", test.ID, "required test did not pass"))
		}
	}

	return violations
}

func validateTDDObligationEvidence(atom AtomExecutionContract, evidence TerminalEvidenceInput) []PlanExecutionContractViolation {
	var violations []PlanExecutionContractViolation

	artifactSet := scopePathSet(evidence.TestArtifacts)

	for _, result := range evidence.TDDCommandResults {
		for _, path := range UniqueScopePaths(result.ArtifactPaths) {
			artifactSet[path] = struct{}{}
		}
	}

	for _, obligation := range atom.TDDObligations {
		result, ok := findTDDCommandEvidence(obligation, evidence.TDDCommandResults)
		if !ok {
			violations = append(violations, contractViolation("required_tdd_command_missing", atom.AtomID, "", obligation.ID, "terminal success lacks required TDD command result"))
			continue
		}

		if normalizeCommand(result.Command) != normalizeCommand(obligation.Command) {
			violations = append(violations, contractViolation("tdd_command_changed", atom.AtomID, "", obligation.ID, "terminal evidence changed the required TDD command"))
		}

		if !result.Passed {
			violations = append(violations, contractViolation("required_tdd_command_failed", atom.AtomID, "", obligation.ID, "required TDD command did not pass"))
		}

		requiredArtifacts := scopePathSet(obligation.TestArtifactPaths)
		for _, path := range obligation.TestArtifactPaths {
			if _, ok := artifactSet[path]; !ok {
				violations = append(violations, contractViolation("required_test_artifact_missing", atom.AtomID, path, obligation.ID, "terminal success lacks required TDD test artifact"))
			}
		}

		for _, path := range UniqueScopePaths(result.ArtifactPaths) {
			if len(requiredArtifacts) > 0 {
				if _, ok := requiredArtifacts[path]; !ok {
					violations = append(violations, contractViolation("tdd_test_artifact_changed", atom.AtomID, path, obligation.ID, "terminal evidence reported a different TDD test artifact path"))
				}
			}
		}
	}

	return violations
}

func validateAcceptanceEvidence(atom AtomExecutionContract, evidence TerminalEvidenceInput) []PlanExecutionContractViolation {
	accepted := normalizedStringSet(evidence.AcceptanceEvidenceIDs)

	var violations []PlanExecutionContractViolation

	for _, obligation := range atom.AcceptanceObligations {
		if !obligation.Required || obligation.ID == "" {
			continue
		}

		if _, ok := accepted[obligation.ID]; !ok {
			violations = append(violations, contractViolation("acceptance_evidence_missing", atom.AtomID, "", obligation.ID, "terminal success lacks required acceptance evidence"))
		}
	}

	return violations
}

func validateSourceSatisfiedEvidence(atom AtomExecutionContract, evidence TerminalEvidenceInput) []PlanExecutionContractViolation {
	paths := UniqueScopePaths(evidence.SourceSatisfiedPaths)
	if evidence.SourceSatisfied && len(paths) == 0 {
		paths = append([]string(nil), atom.Scope.RequiredCoveragePaths...)
	}

	if len(paths) == 0 {
		return nil
	}

	requirements := atom.SourceSatisfied.normalized()

	var violations []PlanExecutionContractViolation

	if !requirements.Allowed {
		violations = append(violations, contractViolation("source_satisfied_not_authorized", atom.AtomID, "", "", "terminal evidence used source-satisfied without contract authorization"))
	}

	if requirements.RequireNoChangedPaths && len(evidence.ChangedPaths) > 0 {
		violations = append(violations, contractViolation("source_satisfied_has_changed_paths", atom.AtomID, "", "", "source-satisfied terminal evidence must not include changed paths"))
	}

	sourceViolations := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
		RequiredPaths:        atom.Scope.RequiredCoveragePaths,
		SourceSatisfiedPaths: paths,
		Evidence:             evidence.SourceEvidence,
		ApprovedPaths:        atom.Scope.RequiredCoveragePaths,
		RequireEvidence:      requirements.RequireCurrentStateEvidence,
	})
	for _, violation := range sourceViolations {
		violations = append(violations, contractViolation("source_satisfied_"+violation.Code, atom.AtomID, violation.Path, "", violation.Reason))
	}

	return violations
}

func terminalRequiredPathCoverage(atom AtomExecutionContract, evidence TerminalEvidenceInput) map[string]struct{} {
	out := scopePathSet(evidence.ChangedPaths)
	for _, path := range UniqueScopePaths(evidence.RequiredPathCoveragePaths) {
		out[path] = struct{}{}
	}

	sourceSatisfiedPaths := UniqueScopePaths(evidence.SourceSatisfiedPaths)
	if evidence.SourceSatisfied && len(sourceSatisfiedPaths) == 0 {
		sourceSatisfiedPaths = append([]string(nil), atom.Scope.RequiredCoveragePaths...)
	}

	sourceViolations := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
		RequiredPaths:        atom.Scope.RequiredCoveragePaths,
		SourceSatisfiedPaths: sourceSatisfiedPaths,
		Evidence:             evidence.SourceEvidence,
		ApprovedPaths:        atom.Scope.RequiredCoveragePaths,
		RequireEvidence:      atom.SourceSatisfied.normalized().RequireCurrentStateEvidence,
	})
	for path := range SourceSatisfiedCoveragePathSet(sourceSatisfiedPaths, sourceViolations) {
		out[path] = struct{}{}
	}

	return out
}

func (atom AtomExecutionContract) pathAuthorizedForWrite(path string) bool {
	path = NormalizeScopePath(path)
	if path == "" {
		return false
	}

	if atom.Scope.WriteAllowedExplicit {
		return pathAllowedByPatterns(path, atom.Scope.WriteAllowedPaths) ||
			authorizedNewFilePath(path, atom.AuthorizedNewFiles, atom.AuthorizedNewRoots)
	}

	allowed := append([]string(nil), atom.Scope.RequiredCoveragePaths...)

	allowed = append(allowed, atom.Scope.AllowedCandidatePaths...)

	return containsScopePath(allowed, path) || authorizedNewFilePath(path, atom.AuthorizedNewFiles, atom.AuthorizedNewRoots)
}

func pathAllowedByPatterns(path string, patterns []string) bool {
	path = NormalizeScopePath(path)

	for _, raw := range patterns {
		pattern := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if strings.HasSuffix(pattern, "/**") {
			root := normalizeAuthorizedRoot(strings.TrimSuffix(pattern, "/**"))
			if root != "" && pathWithinRoot(path, root) {
				return true
			}

			continue
		}

		if NormalizeScopePath(pattern) == path {
			return true
		}
	}

	return false
}

func authorizedNewFilePath(path string, files []AuthorizedNewFile, roots []AuthorizedNewRoot) bool {
	path = NormalizeScopePath(path)
	for _, file := range files {
		if file.Path == path {
			return true
		}
	}

	for _, root := range roots {
		if pathWithinRoot(path, root.Root) {
			return true
		}
	}

	return false
}

func pathWithinRoot(path string, root string) bool {
	path = NormalizeScopePath(path)
	root = normalizeAuthorizedRoot(root)

	if path == "" || root == "" {
		return false
	}

	return path == root || strings.HasPrefix(path, root+"/")
}

func findRequiredTestEvidence(test RequiredTest, results []RequiredTestEvidence) (RequiredTestEvidence, bool) {
	for _, result := range results {
		if test.ID != "" && NormalizeStableContractIDPart(result.ID) == test.ID {
			return result, true
		}

		if normalizeCommand(result.Command) == normalizeCommand(test.Command) {
			return result, true
		}
	}

	return RequiredTestEvidence{}, false
}

func findTDDCommandEvidence(obligation TDDObligation, results []TDDCommandEvidence) (TDDCommandEvidence, bool) {
	for _, result := range results {
		if obligation.ID != "" && NormalizeStableContractIDPart(result.ID) == obligation.ID {
			return result, true
		}

		if normalizeCommand(result.Command) == normalizeCommand(obligation.Command) {
			return result, true
		}
	}

	return TDDCommandEvidence{}, false
}

func contractHasAtom(contract PlanExecutionContract, atomID string) bool {
	atomID = NormalizeStableContractIDPart(atomID)
	for _, atom := range contract.Atoms {
		if atom.AtomID == atomID {
			return true
		}
	}

	return false
}

func terminalStatusIsSuccess(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "pass", "passed", "complete", "completed", "done":
		return true
	default:
		return false
	}
}

func atomHasRequiredTestArtifacts(atom AtomExecutionContract) bool {
	for _, obligation := range atom.TDDObligations {
		if len(obligation.TestArtifactPaths) > 0 {
			return true
		}
	}

	return false
}

func normalizeAuthorizedRoot(root string) string {
	root = strings.TrimSpace(strings.ReplaceAll(root, "\\", "/"))
	root = strings.TrimSuffix(root, "/**")
	root = strings.TrimSuffix(root, "/")

	return NormalizeScopePath(root)
}

func normalizeCommand(command string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
}

func normalizeEvidenceKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	return strings.ReplaceAll(kind, "-", "_")
}

func uniqueSortedScopePaths(paths []string) []string {
	out := UniqueScopePaths(paths)
	sort.Strings(out)

	return out
}

func uniqueSortedStrings(values []string) []string {
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

func containsScopePath(paths []string, path string) bool {
	path = NormalizeScopePath(path)
	for _, candidate := range UniqueScopePaths(paths) {
		if candidate == path {
			return true
		}
	}

	return false
}

func normalizedStringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}

	for _, value := range values {
		value = NormalizeStableContractIDPart(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}

	return out
}

func contractViolation(code string, atomID string, path string, obligationID string, message string) PlanExecutionContractViolation {
	return PlanExecutionContractViolation{
		Code:         code,
		AtomID:       NormalizeStableContractIDPart(atomID),
		Path:         NormalizeScopePath(path),
		ObligationID: NormalizeStableContractIDPart(obligationID),
		Message:      message,
	}
}
