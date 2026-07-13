package planartifact

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Plan artifact validation", func() {
	ginkgo.It("aligns handoff readiness with execution permission during submission", func() {
		executable := validPlanArtifactForValidation()
		executable.Handoff = PlanArtifactHandoff{ID: "handoff-1", Status: HandoffStatusReady, Ready: false}
		normalized := NormalizeForSubmit(executable, NormalizeForSubmitOptions{
			PlanID:  "plan-1",
			Created: "2026-06-26T00:00:00Z",
		})
		gomega.Expect(normalized.Handoff.Status).To(gomega.Equal(HandoffStatusReady))
		gomega.Expect(normalized.Handoff.Ready).To(gomega.BeTrue())
		gomega.Expect(normalized.Handoff.Blockers).To(gomega.BeEmpty())

		blocked := validPlanArtifactForValidation()
		blocked.Approval.ExecutionPermission = ExecutionPermissionBlocked
		blocked.Handoff = PlanArtifactHandoff{ID: "handoff-2", Status: HandoffStatusReady, Ready: false}
		normalized = NormalizeForSubmit(blocked, NormalizeForSubmitOptions{
			PlanID:  "plan-2",
			Created: "2026-06-26T00:00:00Z",
		})
		gomega.Expect(normalized.Handoff.Status).To(gomega.Equal(HandoffStatusBlocked))
		gomega.Expect(normalized.Handoff.Ready).To(gomega.BeFalse())
		gomega.Expect(normalized.Handoff.Blockers).NotTo(gomega.BeEmpty())
	})

	ginkgo.It("redacts sensitive prose and drops sensitive paths during submission", func() {
		artifact := validPlanArtifactForValidation()
		artifact.Rationale = "api_key=abcdef123456"
		artifact.Task.Summary = "secret abc123"
		artifact.Packet.ArtifactPath = "secrets/plan.json"
		artifact.Evidence = []PlanArtifactEvidence{{
			Kind:    EvidenceKindPlannerSubmission,
			Path:    "secrets/current-state.md",
			Summary: "authorization: bearer abcdef123456",
		}}
		artifact.Edits = []PlanEdit{{
			Path:    "pkg/feature.go",
			OldText: "old",
			NewText: "new",
			Purpose: "password=abcdef123456",
		}}

		normalized := NormalizeForSubmit(artifact, NormalizeForSubmitOptions{
			PlanID:  "plan-sensitive",
			Created: "2026-06-26T00:00:00Z",
		})
		for _, value := range []string{
			normalized.Rationale,
			normalized.Task.Summary,
			normalized.Evidence[0].Summary,
			normalized.Edits[0].Purpose,
		} {
			gomega.Expect(value).To(gomega.HavePrefix("redacted:"))
		}
		gomega.Expect(normalized.Packet.ArtifactPath).To(gomega.BeEmpty())
		gomega.Expect(normalized.Evidence[0].Path).To(gomega.BeEmpty())
	})

	ginkgo.DescribeTable("rejects divergent handoff readiness",
		func(mutate func(*PlanArtifact), wantError string) {
			artifact := validPlanArtifactForValidation()
			mutate(&artifact)
			gomega.Expect(ValidatePlanArtifact(artifact)).To(gomega.MatchError(gomega.ContainSubstring(wantError)))
		},
		ginkgo.Entry("ready status with false ready flag", func(artifact *PlanArtifact) {
			artifact.Handoff.Status = HandoffStatusReady
			artifact.Handoff.Ready = false
		}, "handoff.ready:invalid_ready_flag"),
		ginkgo.Entry("ready status with blocked execution permission", func(artifact *PlanArtifact) {
			artifact.Approval.ExecutionPermission = ExecutionPermissionBlocked
			artifact.Handoff.Status = HandoffStatusReady
			artifact.Handoff.Ready = true
		}, "handoff.status:not_executable"),
		ginkgo.Entry("blocked status without blockers", func(artifact *PlanArtifact) {
			artifact.Handoff.Status = HandoffStatusBlocked
			artifact.Handoff.Ready = false
			artifact.Handoff.Blockers = nil
		}, "handoff.blockers:required"),
	)

	ginkgo.DescribeTable("classifies single executable edit shapes",
		func(edit PlanEdit, want PlanEditShape) {
			shape, err := ClassifyPlanEditShape(edit)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(shape).To(gomega.Equal(want))
		},
		ginkgo.Entry("substring", PlanEdit{Path: "a.go", OldText: "old", NewText: "new"}, PlanEditShapeSubstring),
		ginkgo.Entry("substring replace all", PlanEdit{Path: "a.go", OldText: "old", NewText: "new", ReplaceAll: true}, PlanEditShapeSubstring),
		ginkgo.Entry("line range", PlanEdit{Path: "a.go", StartLine: 2, EndLine: 4, Content: "replacement\n"}, PlanEditShapeLineRange),
		ginkgo.Entry("line range ref", PlanEdit{Path: "a.go", StartLine: 2, EndLine: 4, ContentRef: "assets/range.txt"}, PlanEditShapeLineRange),
		ginkgo.Entry("insert after line", PlanEdit{Path: "a.go", InsertAfterLine: 2, Content: "inserted\n"}, PlanEditShapeInsertAfterLine),
		ginkgo.Entry("new whole file", PlanEdit{Path: "a.go", NewText: "package a\n", ReplaceAll: true}, PlanEditShapeReplaceAll),
		ginkgo.Entry("new whole file ref", PlanEdit{Path: "a.go", NewTextRef: "assets/body.txt", ReplaceAll: true}, PlanEditShapeReplaceAll),
	)

	ginkgo.DescribeTable("rejects mixed executable edit shapes",
		func(edit PlanEdit, wantError string) {
			gomega.Expect(ValidatePlanEditShape(edit)).To(gomega.MatchError(gomega.ContainSubstring(wantError)))
		},
		ginkgo.Entry("substring plus line range", PlanEdit{Path: "a.go", OldText: "old", NewText: "new", StartLine: 1, EndLine: 1, Content: "replacement\n"}, "mutually exclusive"),
		ginkgo.Entry("substring plus content without range", PlanEdit{Path: "a.go", OldText: "old", NewText: "new", Content: "replacement\n"}, "substring edits are mutually exclusive"),
		ginkgo.Entry("line range plus new text", PlanEdit{Path: "a.go", StartLine: 1, EndLine: 1, Content: "replacement\n", NewText: "new"}, "line-range edits are mutually exclusive"),
		ginkgo.Entry("line range plus replace all", PlanEdit{Path: "a.go", StartLine: 1, EndLine: 1, Content: "replacement\n", NewText: "new", ReplaceAll: true}, "mutually exclusive"),
		ginkgo.Entry("insert plus ref", PlanEdit{Path: "a.go", InsertAfterLine: 1, Content: "inserted\n", ContentRef: "assets/insert.txt"}, "insert_after_line edits do not support content_ref"),
		ginkgo.Entry("insert plus new text", PlanEdit{Path: "a.go", InsertAfterLine: 1, Content: "inserted\n", NewText: "new"}, "insert_after_line edits are mutually exclusive"),
		ginkgo.Entry("whole file plus content", PlanEdit{Path: "a.go", NewText: "package a\n", ReplaceAll: true, Content: "replacement\n"}, "replace_all edits with empty old_text are mutually exclusive"),
	)
})

func validPlanArtifactForValidation() PlanArtifact {
	return PlanArtifact{
		SchemaVersion: SchemaVersion,
		PlanID:        "plan-valid",
		Created:       "2026-06-26T00:00:00Z",
		Task:          PlanArtifactTask{ID: "task-valid"},
		Contract: PlanArtifactContract{
			Kind:              ContractKindPlanThenExecute,
			StrategyOwner:     OwnerModel,
			ContractOwner:     OwnerHarness,
			PermissionProfile: PermissionProfilePlanThenExecute,
			VerificationOwner: OwnerExecutor,
		},
		Packet:   PlanArtifactPacket{Status: PacketStatusNotProvided},
		Paths:    PlanArtifactPaths{Editable: []string{"a.txt"}, Required: []string{"a.txt"}},
		Evidence: []PlanArtifactEvidence{{Kind: EvidenceKindPlannerSubmission, Summary: "bounded test evidence"}},
		VerificationObligations: []PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     VerificationKindExecutorLaneSanity,
			Owner:    OwnerExecutor,
			Status:   VerificationStatusOpen,
			Required: true,
		}},
		Approval: PlanArtifactApproval{
			Required:            false,
			Status:              ApprovalStatusImplicit,
			ScopeID:             "scope-valid",
			ExecutionPermission: ExecutionPermissionExecuteAll,
		},
		Handoff: PlanArtifactHandoff{
			ID:     "handoff-valid",
			From:   OwnerPlanner,
			To:     HandoffTargetPlanExecuteExecutor,
			Status: HandoffStatusReady,
			Ready:  true,
		},
		Edits: []PlanEdit{{
			Path:    "a.txt",
			OldText: "old",
			NewText: "new",
			Purpose: "Update fixture.",
		}},
	}
}
