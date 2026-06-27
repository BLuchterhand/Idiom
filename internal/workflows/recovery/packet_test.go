package recovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"
)

func TestBuildDecompositionPacketCreatesCandidateAtom(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-4",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "internal/workflows/recovery/packet.go"},
		ChangedPaths:      []string{"internal/workflows/recovery/record.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if packet.SchemaVersion != recoverycontract.DecompositionPacketSchemaVersion {
		t.Fatalf("schema=%q", packet.SchemaVersion)
	}

	if packet.NextAction != recoverycontract.NextActionReviewDecomposition {
		t.Fatalf("next action=%q", packet.NextAction)
	}

	if len(packet.CandidateAtoms) != 1 {
		t.Fatalf("candidate atoms=%#v", packet.CandidateAtoms)
	}

	atom := packet.CandidateAtoms[0]
	if atom.ID != "recovery-001" || atom.Complexity != "C1" {
		t.Fatalf("candidate atom=%#v", atom)
	}

	if !stringSlicesEqual(atom.Gates, []string{recoverycontract.GateWorkspaceState, recoverycontract.GateBriefLint, recoverycontract.GateAtomIntake, recoverycontract.GateScope, recoverycontract.GateRequiredTests}) {
		t.Fatalf("gates=%#v", atom.Gates)
	}

	if !stringSlicesEqual(atom.RequiredTests, []string{"go test ./internal/workflows/recovery"}) {
		t.Fatalf("required tests=%#v", atom.RequiredTests)
	}
}

func TestBuildDecompositionPacketRejectsInvalidAuthorityPaths(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-invalid-scope",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"../outside.go", "/tmp/outside.go"},
		ChangedPaths:      []string{"../dirty.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: PartialStateForDirtyPaths(nil, []string{"../dirty.go"}, []string{"../outside.go"}, nil, 1),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if len(packet.CandidateAtoms) != 0 || len(packet.ResidualWork) != 0 {
		t.Fatalf("invalid authority paths must not produce dispatchable atoms: %#v", packet)
	}
	if packet.NextAction != recoverycontract.NextActionInspectFailure {
		t.Fatalf("recoverycontract.NextAction = %q, want %q", packet.NextAction, recoverycontract.NextActionInspectFailure)
	}
}

func TestBuildDecompositionPacketDropsInvalidPathsWithoutExpandingScope(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-mixed-scope",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"pkg/feature.go", "../outside.go", "/tmp/outside.go"},
		ChangedPaths:      []string{"pkg/feature.go", "../dirty.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: PartialStateForDirtyPaths(nil, []string{"pkg/feature.go", "../dirty.go"}, []string{"pkg/feature.go", "../outside.go"}, nil, 1),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if len(packet.CandidateAtoms) != 1 {
		t.Fatalf("candidate atoms=%#v", packet.CandidateAtoms)
	}
	if !stringSlicesEqual(packet.CandidateAtoms[0].RequiredPaths, []string{"pkg/feature.go"}) {
		t.Fatalf("invalid paths survived recovery authority: %#v", packet.CandidateAtoms[0].RequiredPaths)
	}
}

func TestBuildDecompositionPacketPreservesParentAtomProvenance(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-parent-atom",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		ParentAtomID:      "atom-2.1",
		ParentAtomTitle:   "Add parser",
		RequiredEditPaths: []string{"internal/workflows/recovery/record.go"},
		ChangedPaths:      []string{"internal/workflows/recovery/record.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if len(packet.CandidateAtoms) != 1 {
		t.Fatalf("candidate atoms=%#v", packet.CandidateAtoms)
	}

	atom := packet.CandidateAtoms[0]
	if atom.ParentAtomID != "atom-2.1" || atom.OriginalTitle != "Add parser" {
		t.Fatalf("candidate provenance = parent %q title %q", atom.ParentAtomID, atom.OriginalTitle)
	}

	if atom.Title != "Recover Add parser" {
		t.Fatalf("candidate title = %q, want parent-bound recovery title", atom.Title)
	}
}

func TestBuildDecompositionPacketUsesInvariantFailuresBeforeBroadPathInference(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n"), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-invariant-packet",
		FailureKind:       "plan_artifact_invalid",
		FailureReason:     "submit_plan did not satisfy locked coverage",
		RequiredEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
		ChangedPaths:      []string{"pkg/feature_test.go"},
		LockedContract: &recoverycontract.ContractSnapshot{
			ContractID:   "contract_plan_execute_456",
			ContractHash: "sha256:locked",
			SnapshotID:   "snapshot-456",
			SnapshotPath: ".idiom/contracts/contract_plan_execute_456.json",
		},
		InvariantFailures: []recoverycontract.InvariantFailure{{
			InvariantID:            "contract.required_edit_coverage",
			FieldPath:              "submit_plan.edits[0].path",
			Path:                   "pkg/feature.go",
			Expected:               "one valid edit for each locked required path",
			Actual:                 "missing edit for pkg/feature.go",
			RequiredRepairEvidence: []string{"read current source body before repairing"},
		}},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if packet.LockedContract == nil || packet.LockedContract.ContractID != "contract_plan_execute_456" {
		t.Fatalf("packet did not carry locked contract: %#v", packet.LockedContract)
	}

	if len(packet.ResidualWork) != 1 {
		t.Fatalf("residual work=%#v", packet.ResidualWork)
	}

	residual := packet.ResidualWork[0]
	if !stringSlicesEqual(residual.Paths, []string{"pkg/feature.go"}) {
		t.Fatalf("expected invariant path to scope residual before required+changed inference, got %#v", residual.Paths)
	}

	if len(residual.InvariantFailures) != 1 || residual.InvariantFailures[0].InvariantID != "contract.required_edit_coverage" {
		t.Fatalf("residual invariant failures=%#v", residual.InvariantFailures)
	}

	if !evidenceRefsContain(residual.EvidenceRefs, recoverycontract.EvidenceRef{Kind: "invariant_failure", Path: "pkg/feature.go", ID: "contract.required_edit_coverage"}) {
		t.Fatalf("expected invariant evidence ref in %#v", residual.EvidenceRefs)
	}

	if len(packet.CandidateAtoms) != 1 ||
		!stringSlicesEqual(packet.CandidateAtoms[0].RequiredPaths, []string{"pkg/feature.go"}) ||
		!stringSlicesEqual(packet.CandidateAtoms[0].InvariantIDs, []string{"contract.required_edit_coverage"}) {

		t.Fatalf("candidate atom not scoped by invariant failure: %#v", packet.CandidateAtoms)
	}

	brief := RenderRecoveryBrief(root, packet, packet.CandidateAtoms[0])
	for _, want := range []string{
		"Contract ID: `contract_plan_execute_456`",
		"Contract Hash: `sha256:locked`",
		"`contract.required_edit_coverage`",
		"read current source body before repairing",
		"do not broaden scope without a typed authority delta",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}

	if strings.Contains(brief, "`pkg/feature_test.go` |") {
		t.Fatalf("brief should not promote broad changed path when invariant path is available:\n%s", brief)
	}
}

func TestBuildDecompositionPacketUsesDeepestGoPackageForRequiredTests(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-nested",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/runtime/workerruntime/runtime.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if len(packet.CandidateAtoms) != 1 {
		t.Fatalf("candidate atoms=%#v", packet.CandidateAtoms)
	}

	if !stringSlicesEqual(packet.CandidateAtoms[0].RequiredTests, []string{"go test ./internal/runtime/workerruntime"}) {
		t.Fatalf("required tests=%#v", packet.CandidateAtoms[0].RequiredTests)
	}
}

func TestBuildDecompositionPacketAddsResidualEvidenceRefs(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-residual-evidence",
		ProgressStatePath: ".idiom/plan-execute/session-residual-evidence/state.json",
		PlanPath:          ".idiom/plan-execute/session-residual-evidence/plan.json",
		FailureKind:       "verification_failed",
		FailureReason:     "required tests failed",
		RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "internal/workflows/recovery/packet.go"},
		ChangedPaths:      []string{"internal/workflows/recovery/record.go"},
		ExecutorFailures:  []string{"raw_prompt: do not store\nfailing test TestRecovery"},
		ReviewStatus:      "failed",
		ReviewReasonClass: "verification_failed",
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if len(packet.ResidualWork) != 1 {
		t.Fatalf("residual work=%#v", packet.ResidualWork)
	}

	refs := packet.ResidualWork[0].EvidenceRefs
	for _, want := range []recoverycontract.EvidenceRef{
		{Kind: "required_path", Path: "internal/workflows/recovery/record.go"},
		{Kind: "changed_path", Path: "internal/workflows/recovery/record.go"},
		{Kind: "required_path", Path: "internal/workflows/recovery/packet.go"},
		{Kind: "progress_state", Path: ".idiom/plan-execute/session-residual-evidence/state.json"},
		{Kind: "plan_artifact", Path: ".idiom/plan-execute/session-residual-evidence/plan.json"},
		{Kind: "review_repair_status", ID: "failed"},
		{Kind: "review_repair_reason", ID: "verification_failed"},
		{Kind: "required_test", ID: "go test ./internal/workflows/recovery"},
	} {
		if !evidenceRefsContain(refs, want) {
			t.Fatalf("expected residual evidence ref %#v in %#v", want, refs)
		}
	}

	if !evidenceRefsContainKind(refs, "executor_failure") {
		t.Fatalf("expected executor failure evidence in %#v", refs)
	}

	for _, ref := range refs {
		if ref.Summary != "" {
			t.Fatalf("residual evidence refs must be metadata-only, got %#v in %#v", ref, refs)
		}
	}

	if evidenceRefsContainText(refs, "raw_prompt") ||
		evidenceRefsContainText(refs, "do not store") ||
		evidenceRefsContainText(refs, "failing test") {

		t.Fatalf("residual evidence leaked free-form failure text: %#v", refs)
	}
}

func TestBuildDecompositionPacketScopesChangedPathEvidenceToResidualGroup(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:   "session-residual-groups",
		FailureKind: "executor_failed",
		RequiredEditPaths: []string{
			"cmd/idiom/recover.go",
			"internal/surfaces/tui/app/model.go",
			"internal/surfaces/tui/app/product_status.go",
			"internal/workflows/recovery/packet.go",
			"internal/workflows/recovery/record.go",
		},
		ChangedPaths: []string{"internal/surfaces/tui/app/model.go", "internal/workflows/recovery/packet.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 2),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	appResidual, ok := residualWorkForPath(packet, "internal/surfaces/tui/app/model.go")
	if !ok {
		t.Fatalf("missing internal/surfaces/tui/app residual in %#v", packet.ResidualWork)
	}

	if !evidenceRefsContain(appResidual.EvidenceRefs, recoverycontract.EvidenceRef{Kind: "changed_path", Path: "internal/surfaces/tui/app/model.go"}) {
		t.Fatalf("expected app changed-path evidence in %#v", appResidual.EvidenceRefs)
	}

	if evidenceRefsContain(appResidual.EvidenceRefs, recoverycontract.EvidenceRef{Kind: "changed_path", Path: "internal/workflows/recovery/packet.go"}) {
		t.Fatalf("app residual should not cite recovery changed-path evidence: %#v", appResidual.EvidenceRefs)
	}
}

func TestWriteAndLoadDecompositionPacket(t *testing.T) {
	root := t.TempDir()

	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-5",
		FailureKind:       "executor_failed",
		ParentAtomID:      "atom-5",
		ParentAtomTitle:   "Write/load recovery",
		RequiredEditPaths: []string{"a.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	fullPath, relPath, err := WriteDecompositionPacket(root, packet, "")
	if err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	wantRel := filepath.ToSlash(filepath.Join(".idiom", "recovery", record.FailureID, "packet.json"))
	if relPath != wantRel {
		t.Fatalf("rel path=%q want %q", relPath, wantRel)
	}

	loaded, _, found, err := TryLoadDecompositionPacket(fullPath)
	if err != nil || !found {
		t.Fatalf("TryLoadDecompositionPacket found=%v err=%v", found, err)
	}

	if loaded.PacketID != packet.PacketID || loaded.FailureID != packet.FailureID {
		t.Fatalf("loaded packet mismatch: %#v", loaded)
	}

	if len(loaded.CandidateAtoms) != 1 ||
		loaded.CandidateAtoms[0].ParentAtomID != "atom-5" ||
		loaded.CandidateAtoms[0].OriginalTitle != "Write/load recovery" {
		t.Fatalf("loaded candidate provenance mismatch: %#v", loaded.CandidateAtoms)
	}
}

func TestTryLoadDecompositionPacketRejectsInvalidDurableContractValues(t *testing.T) {
	root := t.TempDir()
	packetPath := filepath.Join(root, ".idiom", "recovery", "plan-execute-invalid-packet", "packet.json")
	if err := os.MkdirAll(filepath.Dir(packetPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{
  "schema_version":"idiom.recovery.decomposition_packet.v1",
  "packet_id":"packet-plan-execute-invalid-packet",
  "failure_id":"plan-execute-invalid-packet",
  "source_run":{"lane":"plan-execute","session_id":"invalid-packet"},
  "failure":{"class":"plan_invalid","summary":"bad packet"},
  "partial_state":{"dirty_status":"clean","scope_status":"none"},
  "next_valid_action":"review_decomposition",
  "residual_work":[{"id":"residual-001","title":"Fix","reason":"bad","paths":["pkg/feature.go"]}],
  "candidate_atoms":[{"id":"recovery-001","title":"Fix","required_paths":["../escape.go"],"residual_work_ids":["residual-missing"]}]
}`
	if err := os.WriteFile(packetPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write packet: %v", err)
	}

	if _, _, _, err := TryLoadDecompositionPacket(packetPath); err == nil || !strings.Contains(err.Error(), "invalid recovery decomposition packet") {
		t.Fatalf("TryLoadDecompositionPacket should reject malformed durable packet, got %v", err)
	}
}

func TestWriteDecompositionPacketRejectsExistingTempSymlink(t *testing.T) {
	root := t.TempDir()

	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-temp-symlink",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"a.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	fullPath, _, err := DecompositionPacketPath(root, record.FailureID, "")
	if err != nil {
		t.Fatalf("DecompositionPacketPath: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir packet dir: %v", err)
	}

	controlPath := filepath.Join(filepath.Dir(fullPath), "failure.json")
	before := []byte("failure record must not be overwritten\n")

	if err := os.WriteFile(controlPath, before, 0o600); err != nil {
		t.Fatalf("write control artifact: %v", err)
	}

	if err := os.Symlink(controlPath, fullPath+".tmp"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, _, err := WriteDecompositionPacket(root, packet, ""); err == nil || !strings.Contains(err.Error(), "write recovery decomposition packet") {
		t.Fatalf("expected temp symlink write rejection, got %v", err)
	}

	after, err := os.ReadFile(controlPath)
	if err != nil {
		t.Fatalf("read control artifact: %v", err)
	}

	if string(after) != string(before) {
		t.Fatalf("control artifact should not be overwritten")
	}
}

func TestWriteDecompositionPacketRejectsFinalSymlink(t *testing.T) {
	root := t.TempDir()

	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-final-symlink",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"a.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	fullPath, _, err := DecompositionPacketPath(root, record.FailureID, "")
	if err != nil {
		t.Fatalf("DecompositionPacketPath: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir packet dir: %v", err)
	}

	target := filepath.Join(t.TempDir(), "packet.json")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := os.Symlink(target, fullPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, _, err := WriteDecompositionPacket(root, packet, ""); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected final symlink write rejection, got %v", err)
	}

	if body, err := os.ReadFile(target); err != nil || string(body) != "outside\n" {
		t.Fatalf("symlink target should not be touched, body=%q err=%v", string(body), err)
	}
}

func TestWriteDecompositionPacketRejectsSymlinkedRecoveryDirectory(t *testing.T) {
	root := t.TempDir()

	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-packet-symlink",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"a.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "recovery")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, _, err := WriteDecompositionPacket(root, packet, ""); err == nil || !strings.Contains(err.Error(), "symlinked directories") {
		t.Fatalf("expected symlinked recovery packet rejection, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(outside, record.FailureID, "packet.json")); !os.IsNotExist(err) {
		t.Fatalf("packet should not be written through symlinked recovery dir, stat err=%v", err)
	}
}

func TestBuildWriteAndLoadReconciliation(t *testing.T) {
	root := t.TempDir()

	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "internal/workflows/recovery/packet.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 12, 2, 0, 0, time.UTC),
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     reconciliationVerificationForPacket(packet, "recovery-001"),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	if reconciliation.Status != recoverycontract.ReconciliationStatusRecovered || reconciliation.NextAction != recoverycontract.NextActionNone {
		t.Fatalf("unexpected reconciliation: %#v", reconciliation)
	}

	if reconciliation.PacketDigest == "" || reconciliation.PacketDigest != PacketDigest(packet) {
		t.Fatalf("unexpected packet digest: %#v want %s", reconciliation, PacketDigest(packet))
	}

	if findings := ValidateReconciliation(packet, reconciliation); len(findings) != 0 {
		t.Fatalf("expected valid reconciliation, got %#v", findings)
	}

	if len(reconciliation.CompletedAtoms) != 1 || reconciliation.CompletedAtoms[0].ID != "recovery-001" {
		t.Fatalf("completed atoms=%#v", reconciliation.CompletedAtoms)
	}

	_, relPath, err := WriteReconciliation(root, reconciliation, "")
	if err != nil {
		t.Fatalf("WriteReconciliation: %v", err)
	}

	wantRel := filepath.ToSlash(filepath.Join(".idiom", "recovery", record.FailureID, "reconciliation.json"))
	if relPath != wantRel {
		t.Fatalf("rel path=%q want %q", relPath, wantRel)
	}

	if _, _, err := WriteReconciliation(root, reconciliation, "logs/reconciliation.json"); err == nil {
		t.Fatal("WriteReconciliation accepted protected reconciliation output path")
	}

	loaded, _, found, err := TryLoadReconciliationForPacket(root, packet)
	if err != nil || !found {
		t.Fatalf("TryLoadReconciliationForPacket found=%v err=%v", found, err)
	}

	if loaded.Status != recoverycontract.ReconciliationStatusRecovered || loaded.CompletedAtoms[0].ID != "recovery-001" {
		t.Fatalf("loaded reconciliation mismatch: %#v", loaded)
	}
}

func TestReconciliationPreservesLockedContractAndBlocksScopeWideningWithoutDelta(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-contract",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"pkg/feature.go"},
		LockedContract: &recoverycontract.ContractSnapshot{
			ContractID:        "contract_plan_execute_789",
			ContractHash:      "sha256:locked-reconcile",
			SnapshotID:        "snapshot-789",
			RuntimeContractID: "runtime-789",
			RequiredEditPaths: []string{"pkg/feature.go"},
		},
		InvariantFailures: []recoverycontract.InvariantFailure{{
			InvariantID:            "contract.scope",
			FieldPath:              "submit_plan.edits[0].path",
			Path:                   "pkg/feature.go",
			Expected:               "path inside locked contract",
			Actual:                 "path drift observed during retry",
			RequiredRepairEvidence: []string{"contract snapshot plus source evidence"},
		}},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     reconciliationVerificationForPacket(packet, "recovery-001"),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	if reconciliation.LockedContract == nil || reconciliation.LockedContract.ContractHash != "sha256:locked-reconcile" {
		t.Fatalf("reconciliation did not preserve locked contract: %#v", reconciliation.LockedContract)
	}

	if len(reconciliation.CompletedAtoms) != 1 ||
		!stringSlicesEqual(reconciliation.CompletedAtoms[0].InvariantIDs, []string{"contract.scope"}) {

		t.Fatalf("reconciliation did not preserve invariant ids: %#v", reconciliation.CompletedAtoms)
	}

	if findings := ValidateReconciliation(packet, reconciliation); len(findings) != 0 {
		t.Fatalf("expected valid reconciliation, got %#v", findings)
	}

	reconciliation.LockedContract.ContractHash = "sha256:changed"

	findings := ValidateReconciliation(packet, reconciliation)

	if !reconciliationFindingsContain(findings, "recovery_reconciliation_contract_mismatch") {
		t.Fatalf("expected contract mismatch finding, got %#v", findings)
	}

	reconciliation.LockedContract.ContractHash = "sha256:locked-reconcile"
	reconciliation.CompletedAtoms[0].RequiredPaths = []string{"pkg/feature.go", "pkg/extra.go"}
	findings = ValidateReconciliation(packet, reconciliation)

	if !reconciliationFindingsContain(findings, "recovery_reconciliation_completed_atom_mismatch") {
		t.Fatalf("expected scope widening finding, got %#v", findings)
	}

	reconciliation.AuthorityDeltas = []recoverycontract.ScopeAuthorityDelta{{
		DeltaID:          "delta-path-only",
		AllowedEditPaths: []string{"pkg/extra.go"},
		Reason:           "path without locked contract identity is not enough",
	}}
	findings = ValidateReconciliation(packet, reconciliation)

	if !reconciliationFindingsContain(findings, "recovery_reconciliation_completed_atom_mismatch") {
		t.Fatalf("path-only authority delta should not widen a locked contract, got %#v", findings)
	}

	reconciliation.AuthorityDeltas = []recoverycontract.ScopeAuthorityDelta{{
		DeltaID:          "delta-add-extra",
		ContractID:       "contract_plan_execute_789",
		ContractHash:     "sha256:locked-reconcile",
		AllowedEditPaths: []string{"pkg/extra.go"},
		Reason:           "operator-authorized retry scope update",
	}}
	findings = ValidateReconciliation(packet, reconciliation)

	if reconciliationFindingsContain(findings, "recovery_reconciliation_completed_atom_mismatch") ||
		reconciliationFindingsContain(findings, "recovery_reconciliation_contract_mismatch") {

		t.Fatalf("authority delta should cover explicit scope widening, got %#v", findings)
	}
}

func TestBuildReconciliationTracksRemainingAtoms(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-partial",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "cmd/idiom/recover.go", "internal/surfaces/tui/app/model.go", "internal/capabilities/workspace/tools.go", "README.md"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if len(packet.CandidateAtoms) < 2 {
		t.Fatalf("expected grouped candidate atoms, got %#v", packet.CandidateAtoms)
	}

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{packet.CandidateAtoms[0].ID},
		Verification:     reconciliationVerificationForPacket(packet, packet.CandidateAtoms[0].ID),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	if reconciliation.Status != recoverycontract.ReconciliationStatusPartial || reconciliation.NextAction != recoverycontract.NextActionReviewDecomposition {
		t.Fatalf("expected partial reconciliation, got %#v", reconciliation)
	}

	if len(reconciliation.RemainingAtoms) != len(packet.CandidateAtoms)-1 {
		t.Fatalf("remaining atoms=%#v packet=%#v", reconciliation.RemainingAtoms, packet.CandidateAtoms)
	}
}

func TestBuildReconciliationRejectsUnknownAtom(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-unknown",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"a.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	if _, err := BuildReconciliation(packet, ReconciliationOptions{CompletedAtomIDs: []string{"missing"}}); err == nil {
		t.Fatal("expected unknown atom error")
	}
}

func TestValidateReconciliationRejectsStaleOrHandEditedArtifact(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-stale",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "internal/workflows/recovery/packet.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     reconciliationVerificationForPacket(packet, "recovery-001"),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	reconciliation.PacketDigest = ""

	findings := ValidateReconciliation(packet, reconciliation)

	if !reconciliationFindingsContain(findings, "recovery_reconciliation_missing_packet_digest") {
		t.Fatalf("expected missing digest finding, got %#v", findings)
	}

	reconciliation.PacketDigest = "sha256:stale"
	findings = ValidateReconciliation(packet, reconciliation)

	if !reconciliationFindingsContain(findings, "recovery_reconciliation_packet_digest_mismatch") {
		t.Fatalf("expected digest mismatch finding, got %#v", findings)
	}

	reconciliation.PacketDigest = PacketDigest(packet)
	reconciliation.CompletedAtoms[0].RequiredPaths = []string{"different.go"}
	findings = ValidateReconciliation(packet, reconciliation)

	if !reconciliationFindingsContain(findings, "recovery_reconciliation_completed_atom_mismatch") {
		t.Fatalf("expected atom mismatch finding, got %#v", findings)
	}
}

func TestValidateReconciliationRequiresVerificationEvidence(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-unverified",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/workflows/recovery/record.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{CompletedAtomIDs: []string{"recovery-001"}})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	findings := ValidateReconciliation(packet, reconciliation)
	if !reconciliationFindingsContain(findings, "recovery_reconciliation_missing_verification") {
		t.Fatalf("expected verification finding, got %#v", findings)
	}
}

func TestValidateReconciliationRejectsPassingStatusWithNonZeroExit(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-nonzero",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/workflows/recovery/record.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	verification := reconciliationVerificationForPacket(packet, "recovery-001")
	if len(verification.Commands) == 0 {
		t.Fatalf("expected required test command in fixture")
	}

	verification.Commands[0].ExitCode = 1

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     verification,
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	findings := ValidateReconciliation(packet, reconciliation)
	if !reconciliationFindingsContain(findings, "recovery_reconciliation_missing_required_tests") {
		t.Fatalf("expected required-test finding, got %#v", findings)
	}
}

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func evidenceRefsContain(refs []recoverycontract.EvidenceRef, want recoverycontract.EvidenceRef) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}

	return false
}

func evidenceRefsContainKind(refs []recoverycontract.EvidenceRef, kind string) bool {
	for _, ref := range refs {
		if ref.Kind == kind {
			return true
		}
	}

	return false
}

func evidenceRefsContainText(refs []recoverycontract.EvidenceRef, text string) bool {
	for _, ref := range refs {
		if strings.Contains(ref.Kind, text) ||
			strings.Contains(ref.Path, text) ||
			strings.Contains(ref.ID, text) ||
			strings.Contains(ref.Summary, text) {

			return true
		}
	}

	return false
}

func residualWorkForPath(packet recoverycontract.DecompositionPacket, path string) (recoverycontract.ResidualWork, bool) {
	for _, residual := range packet.ResidualWork {
		for _, residualPath := range residual.Paths {
			if residualPath == path {
				return residual, true
			}
		}
	}

	return recoverycontract.ResidualWork{}, false
}

func reconciliationFindingsContain(findings []recoverycontract.ReconciliationValidationFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}

	return false
}

func reconciliationVerificationForPacket(packet recoverycontract.DecompositionPacket, atomIDs ...string) recoverycontract.ReconciliationVerification {
	completed := map[string]bool{}
	for _, id := range atomIDs {
		completed[id] = true
	}

	seen := map[string]bool{}

	var (
		commands []recoverycontract.ReconciliationCommandResult
		required []string
	)

	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			continue
		}

		for _, test := range atom.RequiredTests {
			if seen[test] {
				continue
			}

			seen[test] = true
			required = append(required, test)
			commands = append(commands, recoverycontract.ReconciliationCommandResult{
				Command:  test,
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			})
		}
	}

	return recoverycontract.ReconciliationVerification{
		Status:        recoverycontract.ReconciliationVerificationPass,
		RequiredTests: required,
		Commands:      commands,
	}
}
