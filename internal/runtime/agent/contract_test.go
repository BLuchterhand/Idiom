package agent

import (
	"testing"

	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
)

// --- contract parity tests ---
func TestCompareParityReportsMatchingDecisions(t *testing.T) {
	report := CompareParity(ParityInput{
		ContractID:               "contract",
		ProtocolHash:             "protocol",
		SurfaceID:                "visible_tool_manifest",
		InvariantID:              "contract.active_authority",
		OldDecision:              "allow",
		NewDecision:              "allow",
		VerificationStatusBefore: "passed",
		VerificationStatusAfter:  "passed",
		ObligationDeltasBefore:   []string{"source_body:closed"},
		ObligationDeltasAfter:    []string{"source_body:closed"},
	})

	if report.Diverged || report.DivergenceReason != "" {
		t.Fatalf("expected no divergence: %#v", report)
	}
	if report.ContractID != "contract" || report.ProtocolHash != "protocol" || report.SurfaceID != "visible_tool_manifest" {
		t.Fatalf("report dropped provenance: %#v", report)
	}
}

func TestCompareParityReportsDivergenceWithReason(t *testing.T) {
	report := CompareParity(ParityInput{
		ContractID:   "contract",
		ProtocolHash: "protocol",
		SurfaceID:    "final_answer_acceptance",
		InvariantID:  "contract.active_authority",
		OldDecision:  "allow",
		NewDecision:  "reject",
	})

	if !report.Diverged || report.DivergenceReason != "decision_mismatch" {
		t.Fatalf("expected decision divergence: %#v", report)
	}
}

// --- turn contract projection tests ---
func TestTurnContractHasActiveAuthority(t *testing.T) {
	if (TurnContract{ContractID: "contract", ProtocolHash: "hash", InvariantIDs: []string{"contract.active_authority"}}).HasActiveAuthority() != true {
		t.Fatal("expected active authority when contract and protocol refs are present")
	}
	if (TurnContract{ContractID: "contract"}).HasActiveAuthority() {
		t.Fatal("expected missing protocol ref to be inactive")
	}
}

func TestTurnContractWithProtocolHash(t *testing.T) {
	contract := TurnContract{ContractID: "contract", ProtocolHash: "old"}

	got := contract.WithProtocolHash("new")
	if got.ProtocolHash != "new" {
		t.Fatalf("ProtocolHash = %q, want new", got.ProtocolHash)
	}
	if got := contract.WithProtocolHash(""); got.ProtocolHash != "old" {
		t.Fatalf("empty protocol hash should preserve existing value: %#v", got)
	}
}

func TestTurnContractFromCompileResultProjectsCanonicalAuthority(t *testing.T) {
	result := contractcompile.CompileResult{
		Contract: autonomy.AutonomyContract{
			ContractID:    "contract-1",
			SchemaVersion: autonomy.ContractEnvelopeSchemaVersion,
			Scope: autonomy.ContractScope{
				Phase: "planner",
			},
			Obligations: []autonomy.Obligation{
				{ObligationID: "source_body"},
				{ObligationID: "submit_plan"},
			},
		},
		Protocol: contractcompile.ModelProtocolProjection{
			ContractID:        "contract-1",
			ProtocolHash:      "sha256:protocol",
			PhaseID:           "plan_execute.required_source",
			ActiveInvariantID: contractcompile.InvariantActiveAuthority,
		},
		ActiveAuthority: contractcompile.ActiveAuthorityBinding{
			ContractID:        "contract-1",
			RuntimeContractID: "runtime-1",
			ProtocolHash:      "sha256:protocol",
			ActiveInvariantID: contractcompile.InvariantActiveAuthority,
		},
	}

	got := FromCompileResult(result)
	if got.ContractID != "contract-1" || got.ProtocolHash != "sha256:protocol" || got.PhaseID != "plan_execute.required_source" {
		t.Fatalf("projection ids = %#v", got)
	}
	if !got.HasActiveAuthority() {
		t.Fatalf("expected active canonical authority: %#v", got)
	}
	if got.Canonical.ContractID != "contract-1" || got.Protocol.ProtocolHash != "sha256:protocol" || got.ActiveAuthority.RuntimeContractID != "runtime-1" {
		t.Fatalf("canonical fields not preserved: %#v", got)
	}
	if len(got.ObligationIDs) != 2 || got.ObligationIDs[0] != "source_body" || got.ObligationIDs[1] != "submit_plan" {
		t.Fatalf("ObligationIDs = %#v", got.ObligationIDs)
	}
}
