package intentloop

import (
	plancontract "idiom/internal/contracts/plan"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Gap analysis", func() {
	ginkgo.It("uses the full detail hash in a fingerprint", func() {
		truncated := "same first 2000 bytes"
		gapA := EvidenceGap{
			Requirement: "verification:go test ./...",
			Status:      "failed",
			Detail:      truncated,
			DetailHash:  sha256Prefixed(truncated + " A"),
		}
		gapB := EvidenceGap{
			Requirement: "verification:go test ./...",
			Status:      "failed",
			Detail:      truncated,
			DetailHash:  sha256Prefixed(truncated + " B"),
		}

		fingerprintA := GapFingerprint([]EvidenceGap{gapA})
		fingerprintB := GapFingerprint([]EvidenceGap{gapB})
		gomega.Expect(fingerprintA).NotTo(gomega.Equal(fingerprintB))
	})

	ginkgo.It("is independent of gap order", func() {
		gaps := []EvidenceGap{
			{Requirement: "verification:go test ./...", Status: "failed", Detail: "b"},
			{Requirement: "source_sanity", Status: "failed", Detail: "a"},
		}
		reversed := []EvidenceGap{gaps[1], gaps[0]}
		gomega.Expect(GapFingerprint(gaps)).To(gomega.Equal(GapFingerprint(reversed)))
	})

	ginkgo.It("blocks a mismatched child identity with the differing field", func() {
		saved := &ChildRunIdentity{
			WorkspaceRootSHA256:        "root",
			ScopeContractSHA256:        "scope-a",
			VerificationCommandsSHA256: "commands",
			ToolManifestSHA256:         "tools",
			BackendProfile:             "profile",
			PlannerModel:               "planner",
			ExecutorModel:              "executor",
		}
		current := *saved
		current.ScopeContractSHA256 = "scope-b"

		record := CycleRecord{
			Status:        "dispatched",
			ChildIdentity: saved,
		}
		outcome := ClassifyCycleOutcome(record, &current)
		gomega.Expect(outcome.Blocking).To(gomega.BeTrue())
		gomega.Expect(outcome.Reason).To(gomega.Equal("blocked_child_identity_mismatch:scope_contract_sha256"))
	})

	ginkgo.DescribeTable("uses the typed plan failure kind as the blocking reason",
		func(failureKind plancontract.PlanExecuteFailureKind) {
			outcome := ClassifyCycleOutcome(CycleRecord{FailureKind: string(failureKind)}, nil)
			gomega.Expect(outcome.Blocking).To(gomega.BeTrue())
			gomega.Expect(outcome.Reason).To(gomega.Equal(string(failureKind)))
		},
		ginkgo.Entry("planner blocked", plancontract.PlanExecuteFailurePlannerBlocked),
		ginkgo.Entry("plan artifact missing", plancontract.PlanExecuteFailurePlanArtifactMissing),
		ginkgo.Entry("plan artifact invalid", plancontract.PlanExecuteFailurePlanArtifactInvalid),
	)
})
