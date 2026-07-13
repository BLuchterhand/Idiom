package writerroute

import (
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Writer route decisions", func() {
	ginkgo.It("normalizes set-like inputs into a stable route ID", func() {
		d1 := NewDecisionFromParams(DecisionParams{
			Mode:               ModeAgenticGuarded,
			Lane:               " implement ",
			RequiredEditPaths:  []string{" internal/contracts/writerroute/decision.go ", "internal/contracts/writerroute/decision_test.go"},
			EvidencePacketIDs:  []string{"pkt-2", " pkt-1 "},
			ReasonCodes:        []ReasonCode{ReasonSingleFileGuardedWrite, ReasonLocalModelDefault},
			EscalationTriggers: []string{"hosted_review", " stale_anchor "},
			TaskFamily:         " artifact_final_answer ",
		})
		d2 := NewDecisionFromParams(DecisionParams{
			Mode:               ModeAgenticGuarded,
			Lane:               "implement",
			RequiredEditPaths:  []string{"internal/contracts/writerroute/decision_test.go", "internal/contracts/writerroute/decision.go"},
			EvidencePacketIDs:  []string{"pkt-1", "pkt-2"},
			ReasonCodes:        []ReasonCode{ReasonLocalModelDefault, ReasonSingleFileGuardedWrite},
			EscalationTriggers: []string{"stale_anchor", "hosted_review"},
			TaskFamily:         "artifact_final_answer",
		})

		gomega.Expect(d1.RouteID).NotTo(gomega.BeEmpty())
		gomega.Expect(d1.RouteID).To(gomega.Equal(d2.RouteID))
		gomega.Expect(d1.RequiredEditPaths).To(gomega.Equal([]string{
			"internal/contracts/writerroute/decision.go",
			"internal/contracts/writerroute/decision_test.go",
		}))
		gomega.Expect(d1.ReasonCodes).To(gomega.Equal([]string{
			"route.local_model_default",
			"route.single_file_guarded_write",
		}))

		differentMode := NewDecisionFromParams(DecisionParams{
			Mode:       ModePlanSubmission,
			Lane:       "implement",
			TaskFamily: "artifact_final_answer",
		})
		gomega.Expect(differentMode.RouteID).NotTo(gomega.Equal(d1.RouteID))
	})

	ginkgo.It("projects normalized decision data into event fields", func() {
		decision := NewDecisionFromParams(DecisionParams{
			Mode:               ModePlanSubmission,
			Lane:               "plan-execute",
			PlannerModel:       "planner-model",
			ExecutorModel:      "executor-model",
			ReviewerModel:      "reviewer-model",
			ClassifierModel:    "classifier-model",
			EffectiveModel:     "planner-model",
			RoleAliases:        map[string]string{" Planner ": "planner-model", "executor": "executor-model"},
			RiskTier:           "low",
			ScopeTier:          "single_atom",
			VerificationOwner:  VerificationOwnerPlannerOwned,
			RequiredEditPaths:  []string{"b.go", "a.go"},
			EvidencePacketIDs:  []string{"pkt-1"},
			ReasonCodes:        []ReasonCode{ReasonMultiFilePlanExecute},
			EscalationTriggers: []string{"hosted_review"},
			UserOverride:       true,
			TaskFamily:         "plan_then_execute_planner",
		})

		gomega.Expect(decision.EventFields()).To(gomega.Equal(map[string]interface{}{
			"schema_version":      SchemaVersion,
			"route_id":            decision.RouteID,
			"task_family":         "plan_then_execute_planner",
			"writer_mode":         string(ModePlanSubmission),
			"lane":                "plan-execute",
			"planner_model":       "planner-model",
			"executor_model":      "executor-model",
			"reviewer_model":      "reviewer-model",
			"classifier_model":    "classifier-model",
			"effective_model":     "planner-model",
			"role_aliases":        map[string]string{"planner": "planner-model", "executor": "executor-model"},
			"risk_tier":           "low",
			"cost_tier":           "local_first",
			"scope_tier":          "single_atom",
			"verification_owner":  "planner_owned",
			"review_policy":       "local_first",
			"required_edit_paths": []string{"a.go", "b.go"},
			"evidence_packet_ids": []string{"pkt-1"},
			"reason_codes":        []string{"route.multi_file_plan_execute"},
			"escalation_triggers": []string{"hosted_review"},
			"user_override":       true,
		}))
	})

	ginkgo.It("applies local-first single-atom defaults", func() {
		decision := NewDecisionFromParams(DecisionParams{
			Mode:              ModeAgenticGuarded,
			Lane:              "implement",
			RequiredEditPaths: []string{"main.go"},
		})

		gomega.Expect(decision.SchemaVersion).To(gomega.Equal(SchemaVersion))
		gomega.Expect(decision.CostTier).To(gomega.Equal("local_first"))
		gomega.Expect(decision.ReviewPolicy).To(gomega.Equal("local_first"))
		gomega.Expect(decision.ScopeTier).To(gomega.Equal("single_atom"))
		gomega.Expect(decision.RouteID).NotTo(gomega.BeEmpty())
	})

	ginkgo.DescribeTable("exposes stable writer mode values",
		func(mode WriterMode, want string) {
			gomega.Expect(string(mode)).To(gomega.Equal(want))
		},
		ginkgo.Entry("agentic guarded", ModeAgenticGuarded, "agentic_guarded"),
		ginkgo.Entry("plan submission", ModePlanSubmission, "plan_submission"),
		ginkgo.Entry("deterministic artifact apply", ModeDeterministicArtifactApply, "deterministic_artifact_apply"),
		ginkgo.Entry("host-materialized artifact", ModeHostMaterializedArtifact, "host_materialized_artifact"),
		ginkgo.Entry("read only", ModeReadOnly, "read_only"),
	)

	ginkgo.It("builds command event names only from declared surfaces and events", func() {
		name, err := CommandEventName(CommandSurfaceImplement, EventWriterRouteDecision)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(name).To(gomega.Equal("idiom.implement.writer_route_decision"))

		_, err = CommandEventName(CommandSurface("idiom.unknown"), EventWriterRouteDecision)
		gomega.Expect(err).To(gomega.HaveOccurred())

		_, err = CommandEventName(CommandSurfaceLane, EventType("not_real"))
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("keeps non-empty event fields aligned with JSON fields", func() {
		decision := NewDecisionFromParams(DecisionParams{
			Mode:               ModeAgenticGuarded,
			Lane:               "implement",
			PlannerModel:       "qwen-2.5-72b",
			ExecutorModel:      "qwen-2.5-72b",
			CostTier:           "local_first",
			ReviewPolicy:       "local_first",
			ScopeTier:          "single_atom",
			VerificationOwner:  VerificationOwnerExecutor,
			RequiredEditPaths:  []string{"main.go"},
			EvidencePacketIDs:  []string{"pkt-1"},
			ReasonCodes:        []ReasonCode{ReasonSingleFileGuardedWrite},
			EscalationTriggers: []string{"stale_anchor"},
			UserOverride:       true,
			TaskFamily:         "artifact_final_answer",
		})

		data, err := json.Marshal(decision)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var jsonFields map[string]interface{}
		gomega.Expect(json.Unmarshal(data, &jsonFields)).To(gomega.Succeed())

		eventFields := decision.EventFields()
		for key := range jsonFields {
			gomega.Expect(eventFields).To(gomega.HaveKey(key))
		}
		for key, value := range eventFields {
			if !isEmptyEventField(value) {
				gomega.Expect(jsonFields).To(gomega.HaveKey(key))
			}
		}
	})
})

func isEmptyEventField(value interface{}) bool {
	switch v := value.(type) {
	case string:
		return v == ""
	case bool:
		return !v
	case []string:
		return len(v) == 0
	case map[string]string:
		return len(v) == 0
	case nil:
		return true
	default:
		return false
	}
}
