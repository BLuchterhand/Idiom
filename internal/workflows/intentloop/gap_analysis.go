package intentloop

import (
	"sort"
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

type CycleOutcome struct {
	Blocking bool
	Reason   string
}

func AnalyzeEvidenceGaps(contract IntentLoopContract, record plancontract.PlanExecuteTerminalCompletionRecord) []EvidenceGap {
	record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)

	var gaps []EvidenceGap
	gaps = append(gaps, terminalAuthorityGaps(record)...)
	if strings.TrimSpace(record.SessionID) == "" {
		gaps = append(gaps, EvidenceGap{
			Requirement: "terminal_completion",
			Status:      "missing",
			Detail:      "plan-execute terminal completion is missing a child session id",
			DetailHash:  sha256Prefixed("plan-execute terminal completion is missing a child session id"),
		})
	}

	if contract.RequireSourceSanity {
		gaps = append(gaps, sourceSanityGaps(record)...)
	}
	if contract.RequireVerification || contract.RequireRequiredTests {
		for _, test := range contract.RequiredTestCommands {
			gap, ok := requiredTestGap(test, record)
			if ok {
				gaps = append(gaps, gap)
			}
		}
	}
	if contract.RequireReview && !reviewPassed(record) {
		detail := firstNonEmpty(record.FailureReason, "review evidence is missing or failed")
		gaps = append(gaps, EvidenceGap{
			Requirement: "review",
			Status:      "failed",
			Detail:      detail,
			DetailHash:  sha256Prefixed(detail),
		})
	}
	gaps = append(gaps, requiredPathCoverageGaps(contract, record)...)
	gaps = append(gaps, acceptanceEvidenceGaps(contract, record)...)

	return normalizeEvidenceGaps(gaps)
}

func terminalAuthorityGaps(record plancontract.PlanExecuteTerminalCompletionRecord) []EvidenceGap {
	var gaps []EvidenceGap

	if strings.TrimSpace(string(record.Status)) == string(plancontract.PlanExecuteProgressStatusFailed) ||
		strings.TrimSpace(string(record.FailureKind)) != "" ||
		strings.TrimSpace(record.FailureReason) != "" {
		detail := firstNonEmpty(record.FailureReason, string(record.FailureKind), string(record.Outcome), "plan-execute terminal completion is failed")
		gaps = append(gaps, EvidenceGap{
			Requirement: "terminal_completion",
			Status:      "failed",
			Detail:      detail,
			DetailHash:  sha256Prefixed(detail),
		})
	}

	for _, failure := range record.ValidationFailures {
		failure = strings.TrimSpace(failure)
		if !terminalValidationFailureBlocksIntentLoop(failure) {
			continue
		}
		detail := "terminal completion validation failure: " + failure
		gaps = append(gaps, EvidenceGap{
			Requirement: "terminal_completion",
			Status:      "failed",
			Detail:      detail,
			DetailHash:  sha256Prefixed(detail),
		})
	}

	return gaps
}

func terminalValidationFailureBlocksIntentLoop(failure string) bool {
	failure = strings.TrimSpace(failure)
	if failure == "" {
		return false
	}

	switch failure {
	case "changed_paths_missing", "changed_path_coverage_missing":
		return false
	default:
		return true
	}
}

func terminalValidationFailuresBlockIntentLoop(failures []string) bool {
	for _, failure := range failures {
		if terminalValidationFailureBlocksIntentLoop(failure) {
			return true
		}
	}
	return false
}

func GapFingerprint(gaps []EvidenceGap) string {
	if len(gaps) == 0 {
		return ""
	}

	ids := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		requirement := strings.TrimSpace(gap.Requirement)
		status := strings.TrimSpace(gap.Status)
		if requirement == "" && status == "" {
			continue
		}

		detailHash := strings.TrimPrefix(strings.TrimSpace(gap.DetailHash), "sha256:")
		if detailHash == "" {
			detailHash = sha256Hex(gap.Detail)
		}
		if len(detailHash) > 8 {
			detailHash = detailHash[:8]
		}
		ids = append(ids, requirement+":"+status+":"+detailHash)
	}

	sort.Strings(ids)
	return strings.Join(ids, "\n")
}

func ClassifyCycleOutcome(record CycleRecord, currentIdentity *ChildRunIdentity) CycleOutcome {
	if mismatch := ChildIdentityMismatch(record.ChildIdentity, currentIdentity); mismatch != "" {
		return CycleOutcome{Blocking: true, Reason: "blocked_child_identity_mismatch:" + mismatch}
	}

	failureKind := strings.TrimSpace(record.FailureKind)
	if planFailureKind, ok := plancontract.ParsePlanExecuteFailureKind(failureKind); ok {
		switch planFailureKind {
		case plancontract.PlanExecuteFailurePlannerBlocked,
			plancontract.PlanExecuteFailurePlanArtifactMissing,
			plancontract.PlanExecuteFailurePlanArtifactInvalid:
			return CycleOutcome{Blocking: true, Reason: failureKind}
		}
	}

	switch failureKind {
	case "permission_denied", "protected_path", "outside_write_envelope", plancontract.VerificationRejectUnsupportedCmd,
		"unknown_tool", "tool_schema_mismatch", "missing_terminal_record", "malformed_terminal_record",
		"planner_blocker", "external_scoped_change":
		return CycleOutcome{Blocking: true, Reason: failureKind}
	}

	return CycleOutcome{}
}

func sourceSanityGaps(record plancontract.PlanExecuteTerminalCompletionRecord) []EvidenceGap {
	var gaps []EvidenceGap
	if len(record.SourceSanity) == 0 && len(record.ChangedPaths) > 0 {
		detail := "source sanity evidence is missing"
		return []EvidenceGap{{
			Requirement: "source_sanity",
			Status:      "missing",
			Detail:      detail,
			DetailHash:  sha256Prefixed(detail),
		}}
	}

	for _, check := range record.SourceSanity {
		if phaseCheckPassed(check) {
			continue
		}
		detail := phaseCheckDetail(check)
		gaps = append(gaps, EvidenceGap{
			Requirement: "source_sanity",
			Status:      "failed",
			Detail:      detail,
			DetailHash:  sha256Prefixed(detail),
			Path:        strings.TrimSpace(check.Path),
		})
	}
	return gaps
}

func requiredTestGap(test plancontract.RequiredTest, record plancontract.PlanExecuteTerminalCompletionRecord) (EvidenceGap, bool) {
	command := strings.TrimSpace(test.Command)
	if command == "" {
		return EvidenceGap{}, false
	}

	for _, check := range record.Verification {
		if strings.TrimSpace(check.Command) != command {
			continue
		}
		if phaseCheckPassed(check) {
			return EvidenceGap{}, false
		}
		detail := phaseCheckDetail(check)
		return EvidenceGap{
			Requirement: "verification:" + command,
			Status:      "failed",
			Detail:      detail,
			DetailHash:  sha256Prefixed(detail),
			Path:        strings.TrimSpace(check.Path),
		}, true
	}

	detail := "required verification command was not run: " + command
	return EvidenceGap{
		Requirement: "verification:" + command,
		Status:      "missing",
		Detail:      detail,
		DetailHash:  sha256Prefixed(detail),
	}, true
}

func requiredPathCoverageGaps(contract IntentLoopContract, record plancontract.PlanExecuteTerminalCompletionRecord) []EvidenceGap {
	covered := map[string]struct{}{}
	for _, path := range record.ChangedPaths {
		if path = plancontract.NormalizeScopePath(path); path != "" {
			covered[path] = struct{}{}
		}
	}
	for _, path := range record.SourceSatisfiedEvidencePaths {
		if path = plancontract.NormalizeScopePath(path); path != "" {
			covered[path] = struct{}{}
		}
	}
	for _, item := range record.RequiredPathCoverage {
		if item.Status != "missing" {
			if path := plancontract.NormalizeScopePath(item.Path); path != "" {
				covered[path] = struct{}{}
			}
		}
	}

	var gaps []EvidenceGap
	for _, path := range contract.ScopeContract.RequiredCoveragePaths {
		path = plancontract.NormalizeScopePath(path)
		if path == "" {
			continue
		}
		if _, ok := covered[path]; ok {
			continue
		}
		detail := "required path lacks changed-path or source-satisfied coverage: " + path
		gaps = append(gaps, EvidenceGap{
			Requirement: "required_path:" + path,
			Status:      "missing",
			Detail:      detail,
			DetailHash:  sha256Prefixed(detail),
			Path:        path,
		})
	}
	return gaps
}

func acceptanceEvidenceGaps(contract IntentLoopContract, record plancontract.PlanExecuteTerminalCompletionRecord) []EvidenceGap {
	if len(contract.AcceptanceObligations) == 0 {
		return nil
	}

	atom := plancontract.AtomExecutionContract{
		AtomID:                firstNonEmpty(contract.AtomID, "intent-loop"),
		AcceptanceObligations: append([]plancontract.AcceptanceEvidenceRequired(nil), contract.AcceptanceObligations...),
		TerminalEvidence: plancontract.TerminalEvidenceRequirements{
			Explicit: true,
		},
	}
	evidence := plancontract.TerminalEvidenceInput{
		AtomID:                   atom.AtomID,
		TerminalStatus:           "success",
		RequiredTestResults:      []plancontract.RequiredTestEvidence{{Command: "intent-loop-acceptance-evidence", Passed: true}},
		AcceptanceEvidenceIDs:    append([]string(nil), record.AcceptanceEvidenceIDs...),
		AcceptanceEvidenceClaims: append([]plancontract.AcceptanceEvidenceClaim(nil), record.AcceptanceEvidenceClaims...),
	}

	claims := map[string]plancontract.AcceptanceEvidenceClaim{}
	for _, claim := range record.AcceptanceEvidenceClaims {
		if id := strings.TrimSpace(claim.ID); id != "" {
			claims[id] = claim
		}
	}

	var gaps []EvidenceGap
	for _, violation := range plancontract.ValidateAtomTerminalEvidenceInput(atom, evidence) {
		id := strings.TrimSpace(violation.ObligationID)
		if id == "" {
			continue
		}
		status := "missing"
		thresholdResult := ""
		switch violation.Code {
		case "acceptance_evidence_threshold_not_met", "acceptance_evidence_claim_met_mismatch":
			status = "failed"
			thresholdResult = "not_met"
		case "acceptance_evidence_claim_unparseable", "acceptance_evidence_threshold_unparseable", "acceptance_evidence_threshold_unit_mismatch":
			status = "failed"
			thresholdResult = "unparseable"
		case "acceptance_evidence_claim_missing", "acceptance_evidence_missing":
			thresholdResult = "missing"
		default:
			status = "failed"
		}

		detail := firstNonEmpty(violation.Message, violation.Code)
		gap := EvidenceGap{
			Requirement:     "acceptance:" + id,
			Status:          status,
			Detail:          detail,
			DetailHash:      sha256Prefixed(detail),
			ThresholdResult: thresholdResult,
		}
		if claim, ok := claims[id]; ok {
			gap.Claim = &claim
		}
		gaps = append(gaps, gap)
	}
	return gaps
}

func reviewPassed(record plancontract.PlanExecuteTerminalCompletionRecord) bool {
	if record.ReviewRepairSkipped {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(string(record.ReviewStatus)))
	return status == "passed" || status == "pass" || status == "repaired" || status == "completed"
}

func phaseCheckPassed(check plancontract.PhaseEvidenceCheck) bool {
	status := strings.ToLower(strings.TrimSpace(string(check.Status)))
	return strings.TrimSpace(check.Error) == "" && (status == "ok" || status == "passed" || status == "pass" || status == "success")
}

func phaseCheckDetail(check plancontract.PhaseEvidenceCheck) string {
	return strings.TrimSpace(firstNonEmpty(check.Error, check.Summary, string(check.Status)))
}

func normalizeEvidenceGaps(gaps []EvidenceGap) []EvidenceGap {
	out := make([]EvidenceGap, 0, len(gaps))
	seen := map[string]struct{}{}
	for _, gap := range gaps {
		gap.Requirement = strings.TrimSpace(gap.Requirement)
		gap.Status = strings.TrimSpace(gap.Status)
		gap.Detail = strings.TrimSpace(gap.Detail)
		gap.DetailHash = strings.TrimSpace(gap.DetailHash)
		gap.Path = strings.TrimSpace(gap.Path)
		gap.ThresholdResult = strings.TrimSpace(gap.ThresholdResult)
		if gap.Requirement == "" {
			continue
		}
		if gap.DetailHash == "" {
			gap.DetailHash = sha256Prefixed(gap.Detail)
		}
		key := gap.Requirement + "\x00" + gap.Status + "\x00" + gap.DetailHash
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, gap)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Requirement == out[j].Requirement {
			return out[i].Status < out[j].Status
		}
		return out[i].Requirement < out[j].Requirement
	})
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func ChildIdentityMismatch(saved *ChildRunIdentity, current *ChildRunIdentity) string {
	if saved == nil || current == nil {
		if saved == current {
			return ""
		}
		return "missing_identity"
	}

	checks := []struct {
		name string
		a    string
		b    string
	}{
		{"workspace_root_sha256", saved.WorkspaceRootSHA256, current.WorkspaceRootSHA256},
		{"scope_contract_sha256", saved.ScopeContractSHA256, current.ScopeContractSHA256},
		{"verification_commands_sha256", saved.VerificationCommandsSHA256, current.VerificationCommandsSHA256},
		{"tool_manifest_sha256", saved.ToolManifestSHA256, current.ToolManifestSHA256},
		{"backend_profile", saved.BackendProfile, current.BackendProfile},
		{"planner_model", saved.PlannerModel, current.PlannerModel},
		{"executor_model", saved.ExecutorModel, current.ExecutorModel},
	}

	for _, check := range checks {
		if strings.TrimSpace(check.a) != strings.TrimSpace(check.b) {
			return check.name
		}
	}
	return ""
}
