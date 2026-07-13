package workspace

import (
	"bufio"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
)

type harnessAuditChildLifecycle struct {
	LogsHit                              int                             `json:"logs_hit,omitempty"`
	Records                              int                             `json:"records,omitempty"`
	Invocations                          int                             `json:"invocations,omitempty"`
	Terminals                            int                             `json:"terminals,omitempty"`
	Acceptances                          int                             `json:"acceptances,omitempty"`
	CandidatePatchArtifacts              int                             `json:"candidate_patch_artifacts,omitempty"`
	VerificationAcceptances              int                             `json:"verification_acceptances,omitempty"`
	InvalidRecords                       []harnessAuditChildLifecycleRef `json:"invalid_records,omitempty"`
	InvocationsWithoutTerminal           []harnessAuditChildLifecycleRef `json:"invocations_without_terminal,omitempty"`
	TerminalsWithoutInvocation           []harnessAuditChildLifecycleRef `json:"terminals_without_invocation,omitempty"`
	TerminalsWithoutAcceptance           []harnessAuditChildLifecycleRef `json:"terminals_without_acceptance,omitempty"`
	MissingPatchArtifacts                []harnessAuditChildLifecycleRef `json:"missing_patch_artifacts,omitempty"`
	AcceptancesWithoutInvocation         []harnessAuditChildLifecycleRef `json:"acceptances_without_invocation,omitempty"`
	AcceptancesWithoutTerminal           []harnessAuditChildLifecycleRef `json:"acceptances_without_terminal,omitempty"`
	MismatchedLinks                      []harnessAuditChildLifecycleRef `json:"mismatched_links,omitempty"`
	WeakOutputsAccepted                  []harnessAuditChildLifecycleRef `json:"weak_outputs_accepted,omitempty"`
	VerificationAcceptancesWithoutResult []harnessAuditChildLifecycleRef `json:"verification_acceptances_without_result,omitempty"`
	VerificationAcceptanceViolations     []harnessAuditChildLifecycleRef `json:"verification_acceptance_violations,omitempty"`
}

type harnessAuditChildLifecycleRef struct {
	Kind         string `json:"kind,omitempty"`
	ID           string `json:"id,omitempty"`
	InvocationID string `json:"invocation_id,omitempty"`
	TerminalID   string `json:"terminal_id,omitempty"`
	Reference    string `json:"reference,omitempty"`
	Path         string `json:"path,omitempty"`
	Line         int    `json:"line,omitempty"`
	Problem      string `json:"problem,omitempty"`
}
type harnessAuditChildLifecycleDecoded struct {
	kind         string
	path         string
	line         int
	id           string
	invocationID string
	terminalID   string
	reference    string
	problem      string
	invocation   *autonomy.ParentChildInvocationRecord
	terminal     *autonomy.ChildTerminalRecord
	acceptance   *autonomy.ChildAcceptanceRecord
	patch        *autonomy.CandidatePatchArtifact
	verification *autonomy.VerificationAcceptanceRecord
}

func auditChildLifecycleLogs(paths []string, displayPath func(string) string) *harnessAuditChildLifecycle {
	out := &harnessAuditChildLifecycle{}
	logsHit := map[string]struct{}{}
	invocations := map[string]harnessAuditChildLifecycleDecoded{}
	terminals := map[string]harnessAuditChildLifecycleDecoded{}
	terminalsByInvocation := map[string][]harnessAuditChildLifecycleDecoded{}
	acceptancesByTerminal := map[string][]harnessAuditChildLifecycleDecoded{}
	acceptances := map[string]harnessAuditChildLifecycleDecoded{}
	patchArtifacts := map[string]harnessAuditChildLifecycleDecoded{}
	verificationAcceptances := []harnessAuditChildLifecycleDecoded{}
	verificationResults := map[string]struct{}{}

	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}

		hit := false
		scanner := bufio.NewScanner(file)
		for lineNumber := 1; scanner.Scan(); lineNumber++ {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			for _, ref := range decodeHarnessAuditPlanExecuteVerificationResultRefs([]byte(line)) {
				verificationResults[ref] = struct{}{}
			}

			decoded := decodeHarnessAuditChildLifecycleLine([]byte(line), displayPath(path), lineNumber)
			if len(decoded) == 0 {
				continue
			}

			hit = true
			for _, record := range decoded {
				out.Records++
				if record.problem != "" {
					if record.kind == "verification_acceptance" {
						out.VerificationAcceptanceViolations = append(out.VerificationAcceptanceViolations, record.ref())
					} else {
						out.InvalidRecords = append(out.InvalidRecords, record.ref())
					}
					continue
				}

				switch {
				case record.invocation != nil:
					out.Invocations++
					invocations[record.invocation.InvocationID] = record
				case record.terminal != nil:
					out.Terminals++
					terminals[record.terminal.TerminalID] = record
					terminalsByInvocation[record.terminal.InvocationID] = append(terminalsByInvocation[record.terminal.InvocationID], record)
				case record.acceptance != nil:
					out.Acceptances++
					acceptances[record.acceptance.AcceptanceID] = record
					acceptancesByTerminal[record.acceptance.TerminalID] = append(acceptancesByTerminal[record.acceptance.TerminalID], record)
				case record.patch != nil:
					out.CandidatePatchArtifacts++
					patchArtifacts[record.patch.PatchID] = record
					patchArtifacts["patch:"+record.patch.PatchID] = record
				case record.verification != nil:
					out.VerificationAcceptances++
					verificationAcceptances = append(verificationAcceptances, record)
				}
			}
		}
		_ = file.Close()

		if hit {
			logsHit[path] = struct{}{}
		}
	}

	for invocationID, invocation := range invocations {
		if len(terminalsByInvocation[invocationID]) == 0 {
			out.InvocationsWithoutTerminal = append(out.InvocationsWithoutTerminal, invocation.ref())
		}
	}

	for _, terminal := range terminals {
		invocation, ok := invocations[terminal.terminal.InvocationID]
		if !ok {
			out.TerminalsWithoutInvocation = append(out.TerminalsWithoutInvocation, terminal.ref())
		} else if terminal.terminal.ChildContractID != invocation.invocation.ChildContractID {
			mismatch := terminal.ref()
			mismatch.Problem = "terminal child_contract_id does not match invocation"
			out.MismatchedLinks = append(out.MismatchedLinks, mismatch)
		}
		if childLifecycleTerminalRequiresAcceptance(*terminal.terminal) && len(acceptancesByTerminal[terminal.terminal.TerminalID]) == 0 {
			out.TerminalsWithoutAcceptance = append(out.TerminalsWithoutAcceptance, terminal.ref())
		}
		if terminal.terminal.ResultKind == autonomy.ChildResultCandidatePatch {
			for _, ref := range terminal.terminal.ArtifactRefs {
				artifact, ok := patchArtifacts[ref]
				if !ok {
					missing := terminal.ref()
					missing.Reference = ref
					missing.Problem = "candidate patch terminal references missing patch artifact"
					out.MissingPatchArtifacts = append(out.MissingPatchArtifacts, missing)
					continue
				}
				if artifact.patch.InvocationID != terminal.terminal.InvocationID || artifact.patch.ChildContractID != terminal.terminal.ChildContractID {
					mismatch := terminal.ref()
					mismatch.Reference = ref
					mismatch.Problem = "candidate patch artifact does not match terminal invocation or child contract"
					out.MismatchedLinks = append(out.MismatchedLinks, mismatch)
				}
			}
		}
	}

	for _, acceptance := range acceptances {
		invocation, invocationOK := invocations[acceptance.acceptance.InvocationID]
		if !invocationOK {
			out.AcceptancesWithoutInvocation = append(out.AcceptancesWithoutInvocation, acceptance.ref())
		} else {
			if acceptance.acceptance.ChildContractID != invocation.invocation.ChildContractID {
				mismatch := acceptance.ref()
				mismatch.Problem = "acceptance child_contract_id does not match invocation"
				out.MismatchedLinks = append(out.MismatchedLinks, mismatch)
			}
			if acceptance.acceptance.ParentContractID != invocation.invocation.ParentContractID {
				mismatch := acceptance.ref()
				mismatch.Problem = "acceptance parent_contract_id does not match invocation"
				out.MismatchedLinks = append(out.MismatchedLinks, mismatch)
			}
		}

		terminal, terminalOK := terminals[acceptance.acceptance.TerminalID]
		if !terminalOK {
			out.AcceptancesWithoutTerminal = append(out.AcceptancesWithoutTerminal, acceptance.ref())
			continue
		}
		if acceptance.acceptance.InvocationID != terminal.terminal.InvocationID {
			mismatch := acceptance.ref()
			mismatch.Problem = "acceptance invocation_id does not match terminal"
			out.MismatchedLinks = append(out.MismatchedLinks, mismatch)
		}
		if acceptance.acceptance.ChildContractID != terminal.terminal.ChildContractID {
			mismatch := acceptance.ref()
			mismatch.Problem = "acceptance child_contract_id does not match terminal"
			out.MismatchedLinks = append(out.MismatchedLinks, mismatch)
		}

		if acceptance.acceptance.Decision == autonomy.ChildAcceptanceAccepted &&
			terminal.terminal.ObjectiveAlignment != autonomy.ChildObjectiveAccepted {

			weak := acceptance.ref()
			weak.Problem = fmt.Sprintf("accepted %s terminal with objective_alignment=%s", terminal.terminal.ResultKind, terminal.terminal.ObjectiveAlignment)
			out.WeakOutputsAccepted = append(out.WeakOutputsAccepted, weak)
		}
	}

	for _, verification := range verificationAcceptances {
		if harnessAuditVerificationAcceptanceHasResult(verification.verification, verificationResults) {
			continue
		}
		missing := verification.ref()
		missing.Reference = verification.verification.ResultRef
		missing.Problem = "verification_acceptance has no matching plan-execute verification result event"
		out.VerificationAcceptancesWithoutResult = append(out.VerificationAcceptancesWithoutResult, missing)
	}

	out.LogsHit = len(logsHit)
	if out.Records == 0 {
		return nil
	}

	sortHarnessAuditChildLifecycleRefs(out.InvalidRecords)
	sortHarnessAuditChildLifecycleRefs(out.InvocationsWithoutTerminal)
	sortHarnessAuditChildLifecycleRefs(out.TerminalsWithoutInvocation)
	sortHarnessAuditChildLifecycleRefs(out.TerminalsWithoutAcceptance)
	sortHarnessAuditChildLifecycleRefs(out.MissingPatchArtifacts)
	sortHarnessAuditChildLifecycleRefs(out.AcceptancesWithoutInvocation)
	sortHarnessAuditChildLifecycleRefs(out.AcceptancesWithoutTerminal)
	sortHarnessAuditChildLifecycleRefs(out.MismatchedLinks)
	sortHarnessAuditChildLifecycleRefs(out.WeakOutputsAccepted)
	sortHarnessAuditChildLifecycleRefs(out.VerificationAcceptancesWithoutResult)
	sortHarnessAuditChildLifecycleRefs(out.VerificationAcceptanceViolations)

	return out
}

func decodeHarnessAuditChildLifecycleLine(line []byte, path string, lineNumber int) []harnessAuditChildLifecycleDecoded {
	var out []harnessAuditChildLifecycleDecoded
	if record, ok := decodeHarnessAuditChildLifecycleRaw(line, path, lineNumber); ok {
		out = append(out, record)
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(line, &wrapper); err != nil {
		return out
	}

	if record, ok := decodeHarnessAuditPlanExecuteInvalidChildLifecycleRecord(wrapper, path, lineNumber); ok {
		out = append(out, record)
	}

	role := auditStringFromRaw(wrapper["role"])
	if role == "" {
		return out
	}

	rawContent := wrapper["content"]
	if len(rawContent) == 0 {
		return out
	}

	if role == "child_lifecycle_record_invalid" {
		out = append(out, decodeHarnessAuditInvalidChildLifecycleRecord(rawContent, path, lineNumber))
		return out
	}

	if !harnessAuditChildLifecycleRole(role) {
		return out
	}

	var content string
	if err := json.Unmarshal(rawContent, &content); err == nil {
		if record, ok := decodeHarnessAuditChildLifecycleRaw([]byte(content), path, lineNumber); ok {
			out = append(out, record)
		}
		return out
	}

	if record, ok := decodeHarnessAuditChildLifecycleRaw(rawContent, path, lineNumber); ok {
		out = append(out, record)
	}

	return out
}

func decodeHarnessAuditPlanExecuteInvalidChildLifecycleRecord(wrapper map[string]json.RawMessage, path string, lineNumber int) (harnessAuditChildLifecycleDecoded, bool) {
	eventType := auditStringFromRaw(wrapper["type"])
	kindEvent, ok := plancontract.ParsePlanExecuteInvalidEventName(eventType)
	if !ok {
		return harnessAuditChildLifecycleDecoded{}, false
	}

	kind := string(kindEvent)
	if !harnessAuditChildLifecycleRole(kind) {
		return harnessAuditChildLifecycleDecoded{}, false
	}

	problem := auditStringFromRaw(wrapper["error"])
	if problem == "" {
		problem = "plan-execute emitter rejected lifecycle record"
	}

	return invalidHarnessAuditChildLifecycleRecord(kind, path, lineNumber, problem), true
}

func decodeHarnessAuditPlanExecuteVerificationResultRefs(line []byte) []string {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(line, &wrapper); err != nil {
		return nil
	}
	if auditStringFromRaw(wrapper["type"]) != plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventVerification) {
		return nil
	}

	command := auditStringFromRaw(wrapper["command"])
	if command == "" {
		return nil
	}
	workingDir := auditStringFromRaw(wrapper["working_dir"])
	status := auditStringFromRaw(wrapper["status"])
	summary := auditStringFromRaw(wrapper["summary"])
	errorText := auditStringFromRaw(wrapper["error"])
	resultText := strings.Join([]string{
		"verification",
		command,
		workingDir,
		status,
		summary,
		errorText,
	}, "\x00")

	return []string{
		"command:" + harnessAuditShortHash(command),
		"verification_result:" + harnessAuditShortHash(resultText),
	}
}

func harnessAuditVerificationAcceptanceHasResult(record *autonomy.VerificationAcceptanceRecord, refs map[string]struct{}) bool {
	if record == nil {
		return false
	}
	if _, ok := refs[strings.TrimSpace(record.CommandRef)]; !ok {
		return false
	}
	if _, ok := refs[strings.TrimSpace(record.ResultRef)]; !ok {
		return false
	}
	return true
}

func harnessAuditShortHash(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

func decodeHarnessAuditChildLifecycleRaw(raw []byte, path string, lineNumber int) (harnessAuditChildLifecycleDecoded, bool) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return harnessAuditChildLifecycleDecoded{}, false
	}

	switch probe.SchemaVersion {
	case autonomy.ParentChildInvocationSchemaVersion:
		var record autonomy.ParentChildInvocationRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return invalidHarnessAuditChildLifecycleRecord("parent_child_invocation", path, lineNumber, err.Error()), true
		}
		record.Normalize()
		decoded := harnessAuditChildLifecycleDecoded{kind: "parent_child_invocation", path: path, line: lineNumber, id: record.InvocationID, invocationID: record.InvocationID, invocation: &record}
		if err := record.Validate(); err != nil {
			decoded.problem = err.Error()
		}
		return decoded, true
	case autonomy.ChildTerminalSchemaVersion:
		var record autonomy.ChildTerminalRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return invalidHarnessAuditChildLifecycleRecord("child_terminal", path, lineNumber, err.Error()), true
		}
		record.Normalize()
		decoded := harnessAuditChildLifecycleDecoded{kind: "child_terminal", path: path, line: lineNumber, id: record.TerminalID, invocationID: record.InvocationID, terminalID: record.TerminalID, terminal: &record}
		if err := record.Validate(); err != nil {
			decoded.problem = err.Error()
		}
		return decoded, true
	case autonomy.ChildAcceptanceSchemaVersion:
		var record autonomy.ChildAcceptanceRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return invalidHarnessAuditChildLifecycleRecord("child_acceptance", path, lineNumber, err.Error()), true
		}
		record.Normalize()
		decoded := harnessAuditChildLifecycleDecoded{kind: "child_acceptance", path: path, line: lineNumber, id: record.AcceptanceID, invocationID: record.InvocationID, terminalID: record.TerminalID, acceptance: &record}
		if err := record.Validate(); err != nil {
			decoded.problem = err.Error()
		}
		return decoded, true
	case autonomy.CandidatePatchArtifactSchemaVersion:
		var record autonomy.CandidatePatchArtifact
		if err := json.Unmarshal(raw, &record); err != nil {
			return invalidHarnessAuditChildLifecycleRecord("candidate_patch_artifact", path, lineNumber, err.Error()), true
		}
		record.Normalize()
		decoded := harnessAuditChildLifecycleDecoded{kind: "candidate_patch_artifact", path: path, line: lineNumber, id: record.PatchID, invocationID: record.InvocationID, reference: "patch:" + record.PatchID, patch: &record}
		if err := record.Validate(); err != nil {
			decoded.problem = err.Error()
		}
		return decoded, true
	case autonomy.VerificationAcceptanceSchemaVersion:
		var record autonomy.VerificationAcceptanceRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return invalidHarnessAuditChildLifecycleRecord("verification_acceptance", path, lineNumber, err.Error()), true
		}
		record.Normalize()
		decoded := harnessAuditChildLifecycleDecoded{kind: "verification_acceptance", path: path, line: lineNumber, id: record.VerificationAcceptanceID, verification: &record}
		if err := record.Validate(); err != nil {
			decoded.problem = err.Error()
		}
		return decoded, true
	default:
		return harnessAuditChildLifecycleDecoded{}, false
	}
}

func decodeHarnessAuditInvalidChildLifecycleRecord(raw json.RawMessage, path string, lineNumber int) harnessAuditChildLifecycleDecoded {
	var content string
	if err := json.Unmarshal(raw, &content); err == nil {
		raw = json.RawMessage(content)
	}

	var fields struct {
		Role  string `json:"role"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &fields)

	kind := strings.TrimSpace(fields.Role)
	if kind == "" {
		kind = "child_lifecycle_record_invalid"
	}
	problem := strings.TrimSpace(fields.Error)
	if problem == "" {
		problem = "child lifecycle emitter rejected record"
	}

	return invalidHarnessAuditChildLifecycleRecord(kind, path, lineNumber, problem)
}

func invalidHarnessAuditChildLifecycleRecord(kind string, path string, lineNumber int, problem string) harnessAuditChildLifecycleDecoded {
	return harnessAuditChildLifecycleDecoded{
		kind:    strings.TrimSpace(kind),
		path:    path,
		line:    lineNumber,
		problem: strings.TrimSpace(problem),
	}
}

func harnessAuditChildLifecycleRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "parent_child_invocation", "child_terminal", "child_acceptance", "candidate_patch_artifact", "verification_acceptance":
		return true
	default:
		return false
	}
}

func childLifecycleTerminalRequiresAcceptance(record autonomy.ChildTerminalRecord) bool {
	return record.ResultKind == autonomy.ChildResultEvidencePacket ||
		record.ResultKind == autonomy.ChildResultPlanArtifact ||
		record.ResultKind == autonomy.ChildResultCandidatePatch ||
		record.ResultKind == autonomy.ChildResultFindings ||
		record.ResultKind == autonomy.ChildResultSourceSatisfied ||
		record.ResultKind == autonomy.ChildResultTypedBlocker ||
		record.ResultKind == autonomy.ChildResultAppliedParentEdit
}

func (r harnessAuditChildLifecycleDecoded) ref() harnessAuditChildLifecycleRef {
	return harnessAuditChildLifecycleRef{
		Kind:         r.kind,
		ID:           r.id,
		InvocationID: r.invocationID,
		TerminalID:   r.terminalID,
		Reference:    r.reference,
		Path:         r.path,
		Line:         r.line,
		Problem:      r.problem,
	}
}

func sortHarnessAuditChildLifecycleRefs(refs []harnessAuditChildLifecycleRef) {
	slices.SortStableFunc(refs, func(a, b harnessAuditChildLifecycleRef) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Line, b.Line); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return cmp.Compare(a.Reference, b.Reference)
	})
}

func childLifecycleFindings(report *harnessAuditChildLifecycle) []harnessAuditFinding {
	if report == nil {
		return nil
	}

	var findings []harnessAuditFinding
	if len(report.InvalidRecords) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "child_lifecycle_invalid_record",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d child lifecycle record(s) failed schema validation", len(report.InvalidRecords)),
			Recommendation: "Fix the emitting boundary; lifecycle records must validate before they are used as audit or replay evidence.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.InvocationsWithoutTerminal) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "child_lifecycle_invocation_without_terminal",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d child invocation(s) have no terminal record", len(report.InvocationsWithoutTerminal)),
			Recommendation: "Ensure every child dispatch emits exactly one terminal outcome on success, failure, parse failure, blocker, or budget exhaustion.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.TerminalsWithoutInvocation) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "child_lifecycle_terminal_without_invocation",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d child terminal record(s) have no matching invocation", len(report.TerminalsWithoutInvocation)),
			Recommendation: "Emit parent_child_invocation before dispatching child work and preserve invocation_id through terminal closeout.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.TerminalsWithoutAcceptance) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "child_lifecycle_terminal_without_acceptance",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d child terminal output(s) can influence parent state without a child_acceptance record", len(report.TerminalsWithoutAcceptance)),
			Recommendation: "Require parent acceptance before child evidence, patches, blockers, or applied edits influence readiness, verification, or final output.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.MissingPatchArtifacts) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "child_lifecycle_missing_patch_artifact",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d candidate patch reference(s) have no candidate_patch_artifact record", len(report.MissingPatchArtifacts)),
			Recommendation: "Emit candidate_patch_artifact records before candidate_patch terminals reference them.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.AcceptancesWithoutInvocation) > 0 || len(report.AcceptancesWithoutTerminal) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "child_lifecycle_acceptance_without_source",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d acceptance(s) missing invocation, %d acceptance(s) missing terminal", len(report.AcceptancesWithoutInvocation), len(report.AcceptancesWithoutTerminal)),
			Recommendation: "Do not accept child output unless the invocation and terminal records are present and linkable.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.MismatchedLinks) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "child_lifecycle_mismatched_link",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d child lifecycle link(s) have mismatched invocation, child contract, parent contract, or terminal IDs", len(report.MismatchedLinks)),
			Recommendation: "Preserve invocation_id, child_contract_id, parent_contract_id, and terminal_id across invocation, terminal, and acceptance records before accepting child output.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.WeakOutputsAccepted) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "child_lifecycle_weak_output_accepted",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d accepted child output(s) were weak or off-objective at terminal closeout", len(report.WeakOutputsAccepted)),
			Recommendation: "Route weak or off-objective child outputs to repair, blocker, or non-authoritative recorded-only handling instead of accepting them.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.VerificationAcceptancesWithoutResult) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "verification_acceptance_without_result",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d verification acceptance record(s) have no matching plan-execute verification command/result event", len(report.VerificationAcceptancesWithoutResult)),
			Recommendation: "Emit verification_acceptance only next to the concrete verification event whose command and result refs it cites.",
			Tools:          []string{"harness_audit"},
		})
	}
	if len(report.VerificationAcceptanceViolations) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "verification_acceptance_invalid",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d verification acceptance record(s) failed verification integrity checks", len(report.VerificationAcceptanceViolations)),
			Recommendation: "Emit verification_acceptance only for real command/result pairs that satisfy the referenced verification obligations.",
			Tools:          []string{"harness_audit"},
		})
	}

	return findings
}
