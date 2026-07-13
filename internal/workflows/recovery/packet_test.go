package recovery

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"

	recoverycontract "idiom/internal/contracts/recovery"
)

var _ = ginkgo.Describe("Recovery decomposition packets", func() {
	ginkgo.Describe("packet construction", func() {
		ginkgo.It("creates a bounded candidate atom for residual work", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-4",
				FailureKind:       "executor_failed",
				FailureReason:     "edit failed",
				RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "internal/workflows/recovery/packet.go"},
				ChangedPaths:      []string{"internal/workflows/recovery/record.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{
				Now:          recoveryPacketTestTime(),
				PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
			})

			gomega.Expect(packet.SchemaVersion).To(gomega.Equal(recoverycontract.DecompositionPacketSchemaVersion))
			gomega.Expect(packet.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition))
			gomega.Expect(packet.CandidateAtoms).To(gomega.HaveLen(1))
			atom := packet.CandidateAtoms[0]
			gomega.Expect(atom.ID).To(gomega.Equal("recovery-001"))
			gomega.Expect(atom.Complexity).To(gomega.Equal("C1"))
			gomega.Expect(atom.Gates).To(gomega.Equal([]string{
				recoverycontract.GateWorkspaceState,
				recoverycontract.GateBriefLint,
				recoverycontract.GateAtomIntake,
				recoverycontract.GateScope,
				recoverycontract.GateRequiredTests,
			}))
			gomega.Expect(atom.RequiredTests).To(gomega.Equal([]string{"go test ./internal/workflows/recovery"}))
		})

		ginkgo.It("does not turn invalid authority paths into dispatchable work", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-invalid-scope",
				FailureKind:       "executor_failed",
				FailureReason:     "edit failed",
				RequiredEditPaths: []string{"../outside.go", "/tmp/outside.go"},
				ChangedPaths:      []string{"../dirty.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{
				Now:          recoveryPacketTestTime(),
				PartialState: PartialStateForDirtyPaths(nil, []string{"../dirty.go"}, []string{"../outside.go"}, nil, 1),
			})

			gomega.Expect(packet.CandidateAtoms).To(gomega.BeEmpty())
			gomega.Expect(packet.ResidualWork).To(gomega.BeEmpty())
			gomega.Expect(packet.NextAction).To(gomega.Equal(recoverycontract.NextActionInspectFailure))
		})

		ginkgo.It("drops invalid paths from mixed authority without broadening scope", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-mixed-scope",
				FailureKind:       "executor_failed",
				FailureReason:     "edit failed",
				RequiredEditPaths: []string{"pkg/feature.go", "../outside.go", "/tmp/outside.go"},
				ChangedPaths:      []string{"pkg/feature.go", "../dirty.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{
				Now:          recoveryPacketTestTime(),
				PartialState: PartialStateForDirtyPaths(nil, []string{"pkg/feature.go", "../dirty.go"}, []string{"pkg/feature.go", "../outside.go"}, nil, 1),
			})

			gomega.Expect(packet.CandidateAtoms).To(gomega.HaveLen(1))
			gomega.Expect(packet.CandidateAtoms[0].RequiredPaths).To(gomega.Equal([]string{"pkg/feature.go"}))
		})

		ginkgo.It("preserves parent atom provenance", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-parent-atom",
				FailureKind:       "executor_failed",
				FailureReason:     "edit failed",
				ParentAtomID:      "atom-2.1",
				ParentAtomTitle:   "Add parser",
				RequiredEditPaths: []string{"internal/workflows/recovery/record.go"},
				ChangedPaths:      []string{"internal/workflows/recovery/record.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{
				Now:          recoveryPacketTestTime(),
				PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
			})

			gomega.Expect(packet.CandidateAtoms).To(gomega.HaveLen(1))
			gomega.Expect(packet.CandidateAtoms[0]).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"ParentAtomID":  gomega.Equal("atom-2.1"),
				"OriginalTitle": gomega.Equal("Add parser"),
				"Title":         gomega.Equal("Recover Add parser"),
			}))
		})

		ginkgo.It("scopes residual work from invariant failures before broad path inference", func() {
			root := ginkgo.GinkgoT().TempDir()
			gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n"), 0o600)).To(gomega.Succeed())

			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
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
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{
				Now:          recoveryPacketTestTime(),
				PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
			})

			gomega.Expect(packet.LockedContract).NotTo(gomega.BeNil())
			gomega.Expect(packet.LockedContract.ContractID).To(gomega.Equal("contract_plan_execute_456"))
			gomega.Expect(packet.ResidualWork).To(gomega.HaveLen(1))
			residual := packet.ResidualWork[0]
			gomega.Expect(residual.Paths).To(gomega.Equal([]string{"pkg/feature.go"}))
			gomega.Expect(residual.InvariantFailures).To(gomega.HaveLen(1))
			gomega.Expect(residual.InvariantFailures[0].InvariantID).To(gomega.Equal("contract.required_edit_coverage"))
			gomega.Expect(residual.EvidenceRefs).To(gomega.ContainElement(recoverycontract.EvidenceRef{
				Kind: "invariant_failure",
				Path: "pkg/feature.go",
				ID:   "contract.required_edit_coverage",
			}))
			gomega.Expect(packet.CandidateAtoms).To(gomega.HaveLen(1))
			gomega.Expect(packet.CandidateAtoms[0].RequiredPaths).To(gomega.Equal([]string{"pkg/feature.go"}))
			gomega.Expect(packet.CandidateAtoms[0].InvariantIDs).To(gomega.Equal([]string{"contract.required_edit_coverage"}))

			brief := RenderRecoveryBrief(root, packet, packet.CandidateAtoms[0])
			for _, expectedDetail := range []string{
				"Contract ID: `contract_plan_execute_456`",
				"Contract Hash: `sha256:locked`",
				"`contract.required_edit_coverage`",
				"read current source body before repairing",
				"do not broaden scope without a typed authority delta",
			} {
				gomega.Expect(brief).To(gomega.ContainSubstring(expectedDetail))
			}
			gomega.Expect(brief).NotTo(gomega.ContainSubstring("`pkg/feature_test.go` |"))
		})

		ginkgo.It("uses the deepest Go package for required tests", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-nested",
				FailureKind:       "executor_failed",
				RequiredEditPaths: []string{"internal/runtime/workerruntime/runtime.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})

			gomega.Expect(packet.CandidateAtoms).To(gomega.HaveLen(1))
			gomega.Expect(packet.CandidateAtoms[0].RequiredTests).To(gomega.Equal([]string{"go test ./internal/runtime/workerruntime"}))
		})

		ginkgo.It("attaches metadata-only evidence to residual work", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
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
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{
				Now:          recoveryPacketTestTime(),
				PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
			})

			gomega.Expect(packet.ResidualWork).To(gomega.HaveLen(1))
			refs := packet.ResidualWork[0].EvidenceRefs
			for _, expectedRef := range []recoverycontract.EvidenceRef{
				{Kind: "required_path", Path: "internal/workflows/recovery/record.go"},
				{Kind: "changed_path", Path: "internal/workflows/recovery/record.go"},
				{Kind: "required_path", Path: "internal/workflows/recovery/packet.go"},
				{Kind: "progress_state", Path: ".idiom/plan-execute/session-residual-evidence/state.json"},
				{Kind: "plan_artifact", Path: ".idiom/plan-execute/session-residual-evidence/plan.json"},
				{Kind: "review_repair_status", ID: "failed"},
				{Kind: "review_repair_reason", ID: "verification_failed"},
				{Kind: "required_test", ID: "go test ./internal/workflows/recovery"},
			} {
				gomega.Expect(refs).To(gomega.ContainElement(expectedRef))
			}
			gomega.Expect(refs).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"Kind": gomega.Equal("executor_failure"),
			})))
			for _, ref := range refs {
				gomega.Expect(ref.Summary).To(gomega.BeEmpty())
				metadata := strings.Join([]string{ref.Kind, ref.Path, ref.ID, ref.Summary}, "\n")
				gomega.Expect(metadata).NotTo(gomega.Or(
					gomega.ContainSubstring("raw_prompt"),
					gomega.ContainSubstring("do not store"),
					gomega.ContainSubstring("failing test"),
				))
			}
		})

		ginkgo.It("scopes changed-path evidence to its residual group", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
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
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{
				Now:          recoveryPacketTestTime(),
				PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 2),
			})

			appResidual, ok := residualWorkForPath(packet, "internal/surfaces/tui/app/model.go")
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(appResidual.EvidenceRefs).To(gomega.ContainElement(recoverycontract.EvidenceRef{
				Kind: "changed_path",
				Path: "internal/surfaces/tui/app/model.go",
			}))
			gomega.Expect(appResidual.EvidenceRefs).NotTo(gomega.ContainElement(recoverycontract.EvidenceRef{
				Kind: "changed_path",
				Path: "internal/workflows/recovery/packet.go",
			}))
		})
	})

	ginkgo.Describe("durable packet writes", func() {
		ginkgo.It("round-trips the packet and parent provenance", func() {
			root := ginkgo.GinkgoT().TempDir()
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-5",
				FailureKind:       "executor_failed",
				ParentAtomID:      "atom-5",
				ParentAtomTitle:   "Write/load recovery",
				RequiredEditPaths: []string{"a.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})

			fullPath, relPath, err := WriteDecompositionPacket(root, packet, "")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(relPath).To(gomega.Equal(filepath.ToSlash(filepath.Join(".idiom", "recovery", record.FailureID, "packet.json"))))

			loaded, _, found, err := TryLoadDecompositionPacket(fullPath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(found).To(gomega.BeTrue())
			gomega.Expect(loaded.PacketID).To(gomega.Equal(packet.PacketID))
			gomega.Expect(loaded.FailureID).To(gomega.Equal(packet.FailureID))
			gomega.Expect(loaded.CandidateAtoms).To(gomega.HaveLen(1))
			gomega.Expect(loaded.CandidateAtoms[0].ParentAtomID).To(gomega.Equal("atom-5"))
			gomega.Expect(loaded.CandidateAtoms[0].OriginalTitle).To(gomega.Equal("Write/load recovery"))
		})

		ginkgo.It("rejects malformed durable packet contracts", func() {
			packetPath := filepath.Join(ginkgo.GinkgoT().TempDir(), ".idiom", "recovery", "plan-execute-invalid-packet", "packet.json")
			gomega.Expect(os.MkdirAll(filepath.Dir(packetPath), 0o755)).To(gomega.Succeed())
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
			gomega.Expect(os.WriteFile(packetPath, []byte(body), 0o600)).To(gomega.Succeed())

			_, _, _, err := TryLoadDecompositionPacket(packetPath)
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("invalid recovery decomposition packet")))
		})

		ginkgo.It("rejects an existing temporary symlink without overwriting its control target", func() {
			root := ginkgo.GinkgoT().TempDir()
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-temp-symlink",
				FailureKind:       "executor_failed",
				RequiredEditPaths: []string{"a.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
			fullPath, _, err := DecompositionPacketPath(root, record.FailureID, "")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed())
			controlPath := filepath.Join(filepath.Dir(fullPath), "failure.json")
			before := []byte("failure record must not be overwritten\n")
			gomega.Expect(os.WriteFile(controlPath, before, 0o600)).To(gomega.Succeed())
			if err := os.Symlink(controlPath, fullPath+".tmp"); err != nil {
				ginkgo.Skip("symlinks unavailable: " + err.Error())
			}

			_, _, err = WriteDecompositionPacket(root, packet, "")
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("write recovery decomposition packet")))
			after, err := os.ReadFile(controlPath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(after).To(gomega.Equal(before))
		})

		ginkgo.It("rejects a final symlink without touching its target", func() {
			root := ginkgo.GinkgoT().TempDir()
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-final-symlink",
				FailureKind:       "executor_failed",
				RequiredEditPaths: []string{"a.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
			fullPath, _, err := DecompositionPacketPath(root, record.FailureID, "")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed())
			target := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")
			gomega.Expect(os.WriteFile(target, []byte("outside\n"), 0o600)).To(gomega.Succeed())
			if err := os.Symlink(target, fullPath); err != nil {
				ginkgo.Skip("symlinks unavailable: " + err.Error())
			}

			_, _, err = WriteDecompositionPacket(root, packet, "")
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("symlink")))
			body, err := os.ReadFile(target)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(body).To(gomega.Equal([]byte("outside\n")))
		})

		ginkgo.It("rejects a symlinked recovery directory without writing through it", func() {
			root := ginkgo.GinkgoT().TempDir()
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-packet-symlink",
				FailureKind:       "executor_failed",
				RequiredEditPaths: []string{"a.go"},
			})
			packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
			outside := ginkgo.GinkgoT().TempDir()
			gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())
			if err := os.Symlink(outside, filepath.Join(root, ".idiom", "recovery")); err != nil {
				ginkgo.Skip("symlinks unavailable: " + err.Error())
			}

			_, _, err := WriteDecompositionPacket(root, packet, "")
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("symlinked directories")))
			_, statErr := os.Stat(filepath.Join(outside, record.FailureID, "packet.json"))
			gomega.Expect(statErr).To(gomega.Satisfy(os.IsNotExist))
		})
	})
})

var _ = ginkgo.Describe("Recovery reconciliation", func() {
	ginkgo.It("builds, validates, writes, and loads a recovered reconciliation", ginkgo.Label("development-scenario", "integration", "contract"), func() {
		root := ginkgo.GinkgoT().TempDir()
		record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
			SessionID:         "session-reconcile",
			FailureKind:       "executor_failed",
			RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "internal/workflows/recovery/packet.go"},
		})
		packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
		reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
			Now:              recoveryPacketTestTime().Add(time.Minute),
			CompletedAtomIDs: []string{"recovery-001"},
			Verification:     reconciliationVerificationForPacket(packet, "recovery-001"),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(reconciliation.Status).To(gomega.Equal(recoverycontract.ReconciliationStatusRecovered))
		gomega.Expect(reconciliation.NextAction).To(gomega.Equal(recoverycontract.NextActionNone))
		gomega.Expect(reconciliation.PacketDigest).To(gomega.Equal(PacketDigest(packet)))
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(gomega.BeEmpty())
		gomega.Expect(reconciliation.CompletedAtoms).To(gomega.HaveLen(1))
		gomega.Expect(reconciliation.CompletedAtoms[0].ID).To(gomega.Equal("recovery-001"))

		_, relPath, err := WriteReconciliation(root, reconciliation, "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(relPath).To(gomega.Equal(filepath.ToSlash(filepath.Join(".idiom", "recovery", record.FailureID, "reconciliation.json"))))
		_, _, err = WriteReconciliation(root, reconciliation, "logs/reconciliation.json")
		gomega.Expect(err).To(gomega.HaveOccurred())

		loaded, _, found, err := TryLoadReconciliationForPacket(root, packet)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(loaded.Status).To(gomega.Equal(recoverycontract.ReconciliationStatusRecovered))
		gomega.Expect(loaded.CompletedAtoms).NotTo(gomega.BeEmpty())
		gomega.Expect(loaded.CompletedAtoms[0].ID).To(gomega.Equal("recovery-001"))
	})

	ginkgo.It("requires a contract-bound authority delta before scope widening", ginkgo.Label("development-scenario", "integration", "contract"), func() {
		record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
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
		})
		packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
		reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
			CompletedAtomIDs: []string{"recovery-001"},
			Verification:     reconciliationVerificationForPacket(packet, "recovery-001"),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(reconciliation.LockedContract).NotTo(gomega.BeNil())
		gomega.Expect(reconciliation.LockedContract.ContractHash).To(gomega.Equal("sha256:locked-reconcile"))
		gomega.Expect(reconciliation.CompletedAtoms).To(gomega.HaveLen(1))
		gomega.Expect(reconciliation.CompletedAtoms[0].InvariantIDs).To(gomega.Equal([]string{"contract.scope"}))
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(gomega.BeEmpty())

		reconciliation.LockedContract.ContractHash = "sha256:changed"
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(containReconciliationFinding("recovery_reconciliation_contract_mismatch"))

		reconciliation.LockedContract.ContractHash = "sha256:locked-reconcile"
		reconciliation.CompletedAtoms[0].RequiredPaths = []string{"pkg/feature.go", "pkg/extra.go"}
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(containReconciliationFinding("recovery_reconciliation_completed_atom_mismatch"))

		reconciliation.AuthorityDeltas = []recoverycontract.ScopeAuthorityDelta{{
			DeltaID:          "delta-path-only",
			AllowedEditPaths: []string{"pkg/extra.go"},
			Reason:           "path without locked contract identity is not enough",
		}}
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(containReconciliationFinding("recovery_reconciliation_completed_atom_mismatch"))

		reconciliation.AuthorityDeltas = []recoverycontract.ScopeAuthorityDelta{{
			DeltaID:          "delta-add-extra",
			ContractID:       "contract_plan_execute_789",
			ContractHash:     "sha256:locked-reconcile",
			AllowedEditPaths: []string{"pkg/extra.go"},
			Reason:           "operator-authorized retry scope update",
		}}
		findings := ValidateReconciliation(packet, reconciliation)
		gomega.Expect(findings).NotTo(containReconciliationFinding("recovery_reconciliation_completed_atom_mismatch"))
		gomega.Expect(findings).NotTo(containReconciliationFinding("recovery_reconciliation_contract_mismatch"))
	})

	ginkgo.It("tracks atoms that remain after a partial reconciliation", func() {
		record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
			SessionID:         "session-reconcile-partial",
			FailureKind:       "executor_failed",
			RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "cmd/idiom/recover.go", "internal/surfaces/tui/app/model.go", "internal/capabilities/workspace/tools.go", "README.md"},
		})
		packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
		gomega.Expect(len(packet.CandidateAtoms)).To(gomega.BeNumerically(">=", 2))

		reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
			CompletedAtomIDs: []string{packet.CandidateAtoms[0].ID},
			Verification:     reconciliationVerificationForPacket(packet, packet.CandidateAtoms[0].ID),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(reconciliation.Status).To(gomega.Equal(recoverycontract.ReconciliationStatusPartial))
		gomega.Expect(reconciliation.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition))
		gomega.Expect(reconciliation.RemainingAtoms).To(gomega.HaveLen(len(packet.CandidateAtoms) - 1))
	})

	ginkgo.It("rejects an unknown completed atom", func() {
		record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
			SessionID:         "session-reconcile-unknown",
			FailureKind:       "executor_failed",
			RequiredEditPaths: []string{"a.go"},
		})
		packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})

		_, err := BuildReconciliation(packet, ReconciliationOptions{CompletedAtomIDs: []string{"missing"}})
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("detects a stale or hand-edited artifact", func() {
		record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
			SessionID:         "session-reconcile-stale",
			FailureKind:       "executor_failed",
			RequiredEditPaths: []string{"internal/workflows/recovery/record.go", "internal/workflows/recovery/packet.go"},
		})
		packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
		reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
			CompletedAtomIDs: []string{"recovery-001"},
			Verification:     reconciliationVerificationForPacket(packet, "recovery-001"),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		reconciliation.PacketDigest = ""
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(containReconciliationFinding("recovery_reconciliation_missing_packet_digest"))
		reconciliation.PacketDigest = "sha256:stale"
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(containReconciliationFinding("recovery_reconciliation_packet_digest_mismatch"))
		reconciliation.PacketDigest = PacketDigest(packet)
		reconciliation.CompletedAtoms[0].RequiredPaths = []string{"different.go"}
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(containReconciliationFinding("recovery_reconciliation_completed_atom_mismatch"))
	})

	ginkgo.It("requires verification evidence", func() {
		record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
			SessionID:         "session-reconcile-unverified",
			FailureKind:       "executor_failed",
			RequiredEditPaths: []string{"internal/workflows/recovery/record.go"},
		})
		packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
		reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{CompletedAtomIDs: []string{"recovery-001"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(containReconciliationFinding("recovery_reconciliation_missing_verification"))
	})

	ginkgo.It("rejects a passing command status paired with a nonzero exit", func() {
		record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
			SessionID:         "session-reconcile-nonzero",
			FailureKind:       "executor_failed",
			RequiredEditPaths: []string{"internal/workflows/recovery/record.go"},
		})
		packet := expectDecompositionPacket(record, DecompositionOptions{Now: recoveryPacketTestTime()})
		verification := reconciliationVerificationForPacket(packet, "recovery-001")
		gomega.Expect(verification.Commands).NotTo(gomega.BeEmpty())
		verification.Commands[0].ExitCode = 1

		reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
			CompletedAtomIDs: []string{"recovery-001"},
			Verification:     verification,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(ValidateReconciliation(packet, reconciliation)).To(containReconciliationFinding("recovery_reconciliation_missing_required_tests"))
	})
})

func expectDecompositionPacket(record recoverycontract.FailureRecord, opts DecompositionOptions) recoverycontract.DecompositionPacket {
	packet, err := BuildDecompositionPacket(record, opts)
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
	return packet
}

func recoveryPacketTestTime() time.Time {
	return time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)
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

func containReconciliationFinding(code string) gomega.OmegaMatcher {
	return gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Code": gomega.Equal(code),
	}))
}

func reconciliationVerificationForPacket(packet recoverycontract.DecompositionPacket, atomIDs ...string) recoverycontract.ReconciliationVerification {
	completed := make(map[string]bool, len(atomIDs))
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
