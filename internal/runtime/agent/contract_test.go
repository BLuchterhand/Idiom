package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
)

// --- contract parity tests ---
var _ = ginkgo.It("CompareParityReportsMatchingDecisions", func() {
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

	gomega.Expect(report.Diverged).To(gomega.BeFalse(), "report=%#v", report)
	gomega.Expect(report.DivergenceReason).To(gomega.BeEmpty(), "report=%#v", report)
	gomega.Expect(report.ContractID).To(gomega.Equal("contract"), "report=%#v", report)
	gomega.Expect(report.ProtocolHash).To(gomega.Equal("protocol"), "report=%#v", report)
	gomega.Expect(report.SurfaceID).To(gomega.Equal("visible_tool_manifest"), "report=%#v", report)
})

var _ = ginkgo.It("CompareParityReportsDivergenceWithReason", func() {
	report := CompareParity(ParityInput{
		ContractID:   "contract",
		ProtocolHash: "protocol",
		SurfaceID:    "final_answer_acceptance",
		InvariantID:  "contract.active_authority",
		OldDecision:  "allow",
		NewDecision:  "reject",
	})

	gomega.Expect(report.Diverged).To(gomega.BeTrue(), "report=%#v", report)
	gomega.Expect(report.DivergenceReason).To(gomega.Equal("decision_mismatch"), "report=%#v", report)
})

// --- turn contract projection tests ---
var _ = ginkgo.It("TurnContractHasActiveAuthority", func() {
	gomega.Expect((TurnContract{
		ContractID:   "contract",
		ProtocolHash: "hash",
		InvariantIDs: []string{"contract.active_authority"},
	}).HasActiveAuthority()).To(gomega.BeTrue())
	gomega.Expect((TurnContract{ContractID: "contract"}).HasActiveAuthority()).To(gomega.BeFalse())
})

var _ = ginkgo.It("TurnContractWithProtocolHash", func() {
	contract := TurnContract{ContractID: "contract", ProtocolHash: "old"}

	got := contract.WithProtocolHash("new")
	gomega.Expect(got.ProtocolHash).To(gomega.Equal("new"))
	gomega.Expect(contract.WithProtocolHash("").ProtocolHash).To(gomega.Equal("old"))
})

var _ = ginkgo.It("TurnContractFromCompileResultProjectsCanonicalAuthority", func() {
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
	gomega.Expect(got.ContractID).To(gomega.Equal("contract-1"), "projection=%#v", got)
	gomega.Expect(got.ProtocolHash).To(gomega.Equal("sha256:protocol"), "projection=%#v", got)
	gomega.Expect(got.PhaseID).To(gomega.Equal("plan_execute.required_source"), "projection=%#v", got)
	gomega.Expect(got.HasActiveAuthority()).To(gomega.BeTrue(), "projection=%#v", got)
	gomega.Expect(got.Canonical.ContractID).To(gomega.Equal("contract-1"), "projection=%#v", got)
	gomega.Expect(got.Protocol.ProtocolHash).To(gomega.Equal("sha256:protocol"), "projection=%#v", got)
	gomega.Expect(got.ActiveAuthority.RuntimeContractID).To(gomega.Equal("runtime-1"), "projection=%#v", got)
	gomega.Expect(got.ObligationIDs).To(gomega.Equal([]string{"source_body", "submit_plan"}))
})
