package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/featureplanner"
)

func TestE2ECreateFullPipeline(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "e2e-feature")

	result, err := InitializeEffort(CreateEffortOptions{
		EffortRoot: root, EffortID: "E2E-001", Title: "End-to-End Feature Test",
	})
	if err != nil {
		t.Fatalf("InitializeEffort: %v", err)
	}
	if result.Status != "ok" || result.CurrentPhase != PhasePRDDrafting {
		t.Fatalf("init: status=%s phase=%s", result.Status, result.CurrentPhase)
	}

	// Write a valid PRD.
	prd := PRDDocument{
		FrontMatter: PRDFrontMatter{
			SchemaVersion: "idiom.prd.v1", PRDID: "PRD-E2E-001",
			Status: string(ArtifactStateDraft), TaskID: "E2E-001",
			CreatedAt: "2026-05-29T00:00:00Z",
		},
		Requirements: []PRDRequirement{
			{ID: "REQ-001", Priority: "P0", Statement: "Users can authenticate with email.",
				EvidenceRefs: []string{"E1"}, TDDObligations: []string{"TDD-001"},
				OwnerAlignment: "Product", Testability: "Login test passes",
				Metric:        "Success rate > 99%",
				OpenQuestions: []string{"MFA?"}, OpenQuestionsPresent: true},
		},
	}
	if err := WritePRDMarkdown(root, prd); err != nil {
		t.Fatalf("write PRD: %v", err)
	}
	if intake := ValidatePRDIntake(prd); !intake.Passed {
		t.Fatalf("PRD intake: %+v", intake.Failures)
	}

	// Approve PRD.
	_, err = AdvanceCreatePhase(root, PhasePRDReview)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	prdResult, prdIntake, err := ApprovePRDGate(root)
	if err != nil || !prdIntake.Passed || prdResult.Status != "ok" {
		t.Fatalf("approve PRD: err=%v passed=%v status=%s", err, prdIntake.Passed, prdResult.Status)
	}

	// Write ADR index + one ADR.
	idx := ADRIndex{
		SchemaVersion: "idiom.adr_index.v1", EffortID: "E2E-001",
		ADRs: []ADRIndexEntry{
			{ADRID: "ADR-001", Title: "Email Auth Strategy", Status: string(ArtifactStateDraft),
				Path: "ADR-001.md", CoverageTier: "unit",
				PRDRefs: []string{"REQ-001"}, CreatedAt: "2026-05-29T00:00:00Z"},
		},
	}
	if err := WriteADRIndexJSON(root, idx); err != nil {
		t.Fatalf("write index: %v", err)
	}
	adr := ADRDocument{
		FrontMatter: ADRFrontMatter{
			SchemaVersion: "idiom.adr.v1", ADRID: "ADR-001",
			Status:  string(ArtifactStateDraft),
			PRDRefs: []string{"REQ-001"}, EvidenceRefs: []string{"E1"},
			TDDObligations: []string{"TDD-001"}, CoverageTier: "unit",
		},
		Sections: ADRSections{
			HasContext: true, HasDecision: true, HasAlternativesConsidered: true,
			HasEvidence: true, HasConsequences: true, HasCompatibilityAndMigration: true,
			HasTDDObligations: true, HasRollback: true, HasOpenQuestions: true,
		},
	}
	if err := WriteADRMarkdown(root, "ADR-001.md", adr); err != nil {
		t.Fatalf("write ADR: %v", err)
	}

	// Approve ADRs.
	_, err = AdvanceCreatePhase(root, PhaseADRReview)
	if err != nil {
		t.Fatalf("advance to adr_review: %v", err)
	}
	adrResult, _, err := ApproveADRGate(root)
	if err != nil || adrResult.Status != "ok" {
		t.Fatalf("approve ADR: err=%v status=%s blockers=%v", err, adrResult.Status, adrResult.Blockers)
	}

	// Write consistent stub files for lint.
	os.WriteFile(filepath.Join(root, "evidence", "evidence.jsonl"), []byte(
		`{"card_id":"E1","kind":"user_research","source_handle":"interview","claim":"Users want email login","confidence":"high","freshness":"fresh","uncertainty":"","evidence_refs":["src"],"researcher_id":"r1","timestamp":"2026-05-29T00:00:00Z"}`+"\n",
	), 0o644)
	os.WriteFile(filepath.Join(root, "evidence", "traceability_matrix.json"), []byte(
		`{"schema_version":"idiom.traceability.v1","effort_id":"E2E-001","task_id":"E2E-001","created_at":"2026-05-29T00:00:00Z","matrix":[{"evidence_card":"E1","prd_requirement":"REQ-001","adr":"ADR-001","tdd_obligation":"TDD-001","brief":"briefs/task.md","report":"reports/impl.json"}]}`+"\n",
	), 0o644)
	os.WriteFile(filepath.Join(root, "briefs", "task.md"), []byte("# Brief\n\nImplement email authentication.\n"), 0o644)
	os.WriteFile(filepath.Join(root, "reports", "impl.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(root, "reports", "review.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(root, "reports", "invest.json"), []byte("{}"), 0o644)

	stubTDD := TDDLedger{
		SchemaVersion: "idiom.tdd_ledger.v1", EffortID: "E2E-001",
		TaskID: "E2E-001", CoverageTier: "unit",
		Coverage: TDDCoverage{MutationMinScore: 80},
		Obligations: []TDDObligation{
			{ID: "TDD-001", Type: "acceptance", Behavior: "Email login flow",
				Owner: "auth.go", Command: "go test ./auth/...",
				RequiredBefore: "implementation", Status: "planned"},
		},
	}
	tddJSON, _ := json.MarshalIndent(stubTDD, "", "  ")
	os.WriteFile(filepath.Join(root, "tdd.json"), append(tddJSON, '\n'), 0o644)
	os.WriteFile(filepath.Join(root, "implementation_ledger.jsonl"), []byte(
		`{"record_type":"atom_planned","task_id":"E2E-001","effort_id":"E2E-001","timestamp":"2026-05-29T00:00:00Z","complexity_tier":"C1","context_pack_id":"cp-1","implementor":{"id":"impl-1","runtime":"local","model":"qwen","command":"make"},"reviewer":{"id":"rev-1","model":"qwen","reasoning_effort":"high","runtime":"local","status":"pending"},"scope":{"expected_files":["auth.go"],"forbidden":["secrets/"]},"required_verification":["go test ./auth/..."],"report_paths":{"implementation":"reports/impl.json","review":"reports/review.json","investigation":"reports/invest.json"}}`+"\n"+
			`{"record_type":"verification_completed","task_id":"E2E-001","effort_id":"E2E-001","timestamp":"2026-05-29T00:00:00Z","tdd_obligations_satisfied":["TDD-001"],"verification_status":"pass","verification_commands_run":["go test ./auth/..."],"actor":"verifier"}`+"\n",
	), 0o644)

	// Lint must pass.
	lintResult := LintEffortFolder(root)
	if lintResult.Status != "pass" {
		t.Errorf("product lint failed:")
		for _, f := range lintResult.Findings {
			t.Errorf("  [%s] %s: %s", f.Code, f.Path, f.Message)
		}
	}

	// Complete the effort.
	_, err = CompleteEffort(root)
	if err != nil {
		t.Fatalf("CompleteEffort: %v", err)
	}

	// ── Feature-planner contract: build brief from disk artifacts ──

	ws, _ := LoadWorkflowState(root)
	if ws.StatusBucket != "closed" {
		t.Fatalf("expected closed, got %s", ws.StatusBucket)
	}

	prdData, _ := os.ReadFile(filepath.Join(root, "prd.md"))
	diskPRD, _ := ParsePRDMarkdown(prdData)

	tddData, _ := os.ReadFile(filepath.Join(root, "tdd.json"))
	diskTDD, _ := ParseTDDLedgerJSON(tddData)

	idxData, _ := os.ReadFile(filepath.Join(root, "adrs", "index.json"))
	diskIdx, _ := ParseADRIndexJSON(idxData)

	var acceptedADRs []ParsedADR
	for _, entry := range diskIdx.ADRs {
		if entry.Status != "accepted" || entry.Path == "" {
			continue
		}
		adrData, _ := os.ReadFile(filepath.Join(root, "adrs", entry.Path))
		adrDoc, _ := ParseADRMarkdown(adrData)
		acceptedADRs = append(acceptedADRs, ParsedADR{
			Doc: adrDoc, RelPath: filepath.Join("adrs", entry.Path),
			RawMarkdown: string(adrData),
		})
	}

	// Parse implementation ledger for TDD evidence coverage.
	ledgerData, _ := os.ReadFile(filepath.Join(root, "implementation_ledger.jsonl"))
	ledgerRecords, _ := ParseImplementationLedgerJSONL(ledgerData)
	knownTDDIDs := BuildKnownTDDObligationIDs(diskTDD, ledgerRecords, "E2E-001")

	var matrix TraceabilityMatrix
	matrixData, _ := os.ReadFile(filepath.Join(root, "evidence", "traceability_matrix.json"))
	matrix, _ = ParseTraceabilityMatrixJSON(matrixData)

	coverage := EvaluateCoverageGate(diskPRD, diskIdx, acceptedADRs, diskTDD, matrix, knownTDDIDs)

	brief := &featureplanner.ProductWorkflowBrief{
		EffortID:           ws.EffortID,
		TaskID:             ws.TaskID,
		EffortRoot:         root,
		CoverageTier:       diskTDD.CoverageTier,
		CoverageGatePassed: coverage.Status == "pass",
		ArtifactRootPath:   root,
	}
	for _, req := range diskPRD.Requirements {
		if req.ID == "" {
			continue
		}
		brief.PRDRequirements = append(brief.PRDRequirements, featureplanner.PRDRequirementBrief{
			ID:          req.ID,
			Priority:    req.Priority,
			Statement:   req.Statement,
			Testability: req.Testability,
			Metric:      req.Metric,
			Obligations: append([]string{}, req.TDDObligations...),
		})
	}
	for _, entry := range diskIdx.ADRs {
		if entry.Status != "accepted" {
			continue
		}
		brief.AcceptedADRIDs = append(brief.AcceptedADRIDs, entry.ADRID)
		brief.AcceptedADRPaths = append(brief.AcceptedADRPaths, filepath.Join("adrs", entry.Path))
	}
	for _, obl := range diskTDD.Obligations {
		if obl.ID == "" {
			continue
		}
		brief.TDDObligations = append(brief.TDDObligations, featureplanner.TDDObligationBrief{
			ID:             obl.ID,
			Behavior:       obl.Behavior,
			Command:        obl.Command,
			RequiredBefore: obl.RequiredBefore,
			Status:         obl.Status,
		})
	}
	for _, b := range coverage.Blockers {
		brief.CoverageBlockers = append(brief.CoverageBlockers, b.Message)
	}

	// Verify the brief contains the artifact data.
	if brief.EffortID != "E2E-001" {
		t.Errorf("brief EffortID: %s", brief.EffortID)
	}
	if !brief.CoverageGatePassed {
		t.Errorf("coverage gate should pass, blockers: %v", brief.CoverageBlockers)
	}
	if len(brief.PRDRequirements) != 1 {
		t.Fatalf("expected 1 PRD requirement in brief, got %d", len(brief.PRDRequirements))
	}
	reqBrief := brief.PRDRequirements[0]
	if reqBrief.ID != "REQ-001" {
		t.Errorf("req ID: %s", reqBrief.ID)
	}
	if reqBrief.Priority != "P0" {
		t.Errorf("req priority: %s", reqBrief.Priority)
	}
	if !strings.Contains(reqBrief.Statement, "authenticate") {
		t.Errorf("req statement missing auth: %s", reqBrief.Statement)
	}
	if len(brief.AcceptedADRIDs) != 1 || brief.AcceptedADRIDs[0] != "ADR-001" {
		t.Errorf("accepted ADRs: %v", brief.AcceptedADRIDs)
	}
	if len(brief.TDDObligations) != 1 || brief.TDDObligations[0].ID != "TDD-001" {
		t.Errorf("TDD obligations: %+v", brief.TDDObligations)
	}

	// ── Feature-planner: prompt generation ──

	state := featureplanner.NewState("e2e-session", "Implement email authentication", "", 3, time.Now())
	prompt := featureplanner.BuildPlannerPrompt("Implement email authentication", state, brief)
	if !strings.Contains(prompt, "REQ-001") {
		t.Error("planner prompt missing REQ-001")
	}
	if !strings.Contains(prompt, "ADR-001") {
		t.Error("planner prompt missing ADR-001")
	}
	if !strings.Contains(prompt, "TDD-001") {
		t.Error("planner prompt missing TDD-001")
	}
	if !strings.Contains(prompt, "authenticate") {
		t.Error("planner prompt missing requirement statement")
	}

	// ── Feature-planner: Run with mock runner ──

	mockPlan := map[string]any{
		"tool":           "submit_feature_plan",
		"schema_version": "idiom.feature_plan.v2",
		"feature_id":     "FEAT-E2E-001",
		"source_task": map[string]any{
			"kind":    "operator_prompt",
			"summary": "Implement email authentication",
		},
		"summary": "Implement email-based authentication flow.",
		"top_level_scope": map[string]any{
			"objective":        "Add email authentication",
			"success_criteria": []string{"Users can log in with email and password"},
		},
		"sdd_contract": map[string]any{
			"prd_requirement_refs": []string{"REQ-001"},
			"adr_refs":             []string{"ADR-001"},
			"acceptance":           []string{"Login acceptance test passes"},
		},
		"tdd_contract": map[string]any{
			"coverage_tier":                  "unit",
			"required_before_implementation": true,
			"coverage_gate_event_id":         "evt-cov-1",
			"obligations": []map[string]any{
				{"id": "TDD-001", "behavior": "Email login flow", "command": "go test ./auth/...", "required_before": "implementation"},
			},
		},
		"atoms": []map[string]any{
			{
				"schema_version":      "idiom.feature_atom.v1",
				"id":                  "atom-1",
				"title":               "Implement email auth",
				"summary":             "Add email/password auth endpoint",
				"required_edit_paths": []string{"internal/auth/login.go"},
				"allowed_edit_paths":  []string{"internal/auth/login.go", "internal/auth/login_test.go"},
				"sdd_refs":            []string{"REQ-001"},
				"tdd_obligations":     []string{"TDD-001"},
				"required_tests":      []string{"go test ./auth/..."},
				"test_first": map[string]any{
					"obligation_refs":               []string{"TDD-001"},
					"commands":                      []string{"go test ./auth/..."},
					"baseline_required":             true,
					"test_artifact_paths":           []string{"auth/login_test.go"},
					"pre_execution_event_ids":       []string{"evt-tdd-1"},
					"expected_pre_execution_result": "red-confirmed",
					"coverage_gate_event_id":        "evt-cov-1",
				},
				"planned_tdd_snapshot": map[string]any{
					"snapshot_id":                      "snap-tdd-1",
					"snapshot_digest":                  "sha256:abc123",
					"obligation_ids":                   []string{"TDD-001"},
					"test_commands":                    []string{"go test ./auth/..."},
					"test_artifact_paths":              []string{"auth/login_test.go"},
					"expected_pre_execution_results":   []string{"red-confirmed"},
					"created_before_dispatch_event_id": "evt-tdd-1",
				},
				"prompt": "Implement email authentication endpoint.",
			},
		},
	}
	mockJSON, _ := json.Marshal(mockPlan)
	mockRunner := func(prompt string, taskFamily string) (string, error) {
		if taskFamily == "feature_planner_planner" {
			return string(mockJSON), nil
		}
		if taskFamily == "feature_planner_implementor" {
			return fmt.Sprintf(`{"tool":%q,"summary":"atom-1 implemented","status":"completed","review_status":"skipped","review_repair_skipped":true}`, featureplanner.ToolPlanExecuteComplete), nil
		}
		return "", fmt.Errorf("unknown task family: %s", taskFamily)
	}

	runResult, runErr := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                "Implement email authentication",
		MaxPlanningIterations: 3,
		MaxAtoms:              5,
		Now:                   time.Now(),
		ProductWorkflowBrief:  brief,
	}, mockRunner)
	if runErr != nil {
		t.Fatalf("featureplanner.Run: %v", runErr)
	}
	if len(runResult.State.Atoms) != 1 {
		t.Fatalf("expected 1 atom, got %d, state status=%s", len(runResult.State.Atoms), runResult.State.Status)
	}
	atom := runResult.State.Atoms[0]
	if atom.ID != "atom-1" {
		t.Errorf("atom ID: %s", atom.ID)
	}
	if len(atom.SDDRefs) != 1 || atom.SDDRefs[0] != "REQ-001" {
		t.Errorf("atom SDD refs: %v", atom.SDDRefs)
	}
	if len(atom.TDDObligations) != 1 || atom.TDDObligations[0] != "TDD-001" {
		t.Errorf("atom TDD obligations: %v", atom.TDDObligations)
	}
	if len(atom.RequiredEditPaths) != 1 || atom.RequiredEditPaths[0] != "internal/auth/login.go" {
		t.Errorf("required_edit_paths: %v", atom.RequiredEditPaths)
	}

	// ── Plan-execute contract: verify atom dispatchability ──

	// The feature-planner produces atoms that plan-execute consumes.
	// Verify every atom has the fields plan-execute requires.
	for i, a := range runResult.State.Atoms {
		if a.ID == "" {
			t.Errorf("atom[%d] missing ID", i)
		}
		if len(a.RequiredEditPaths) == 0 && len(a.AllowedEditPaths) == 0 {
			t.Errorf("atom[%d] (%s) has no edit paths", i, a.ID)
		}
		if len(a.SDDRefs) == 0 {
			t.Errorf("atom[%d] (%s) has no SDD refs", i, a.ID)
		}
	}

	// Verify the events contain both planning and dispatch events.
	var planSubmitCount, dispatchCount int
	for _, e := range runResult.Events {
		switch e.Type {
		case "idiom.feature_planner.plan_accepted":
			planSubmitCount++
		case "idiom.feature_planner.atom_dispatched", "idiom.feature_planner.atom_completed":
			dispatchCount++
		}
	}
	if planSubmitCount == 0 {
		t.Error("no plan_accepted event found")
	}
	if dispatchCount == 0 {
		t.Error("no dispatch event found")
	}
}
