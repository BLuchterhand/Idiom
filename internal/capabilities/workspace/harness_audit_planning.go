package workspace

import (
	"fmt"
	"strings"

	"idiom/internal/evidence/eval"
	"idiom/internal/platform/textutil"
)

type harnessAuditPlanningQuality struct {
	FeedbackTotal       int                 `json:"feedback_total"`
	PlanningFeedback    int                 `json:"planning_feedback"`
	VerifiedFeedback    int                 `json:"verified_feedback,omitempty"`
	HighSeverity        int                 `json:"high_severity,omitempty"`
	PlanningCategories  map[string]int      `json:"planning_categories,omitempty"`
	TopComponents       []harnessAuditCount `json:"top_components,omitempty"`
	RecommendedSurfaces []string            `json:"recommended_surfaces,omitempty"`
}

func planningQualityFromFeedback(feedback *eval.FeedbackStats) *harnessAuditPlanningQuality {
	report := &harnessAuditPlanningQuality{
		PlanningCategories: make(map[string]int),
	}
	if feedback == nil {
		return report
	}

	report.FeedbackTotal = feedback.Total
	report.PlanningFeedback = feedback.PlanningQualityTotal
	report.VerifiedFeedback = feedback.Verified
	report.HighSeverity = feedback.HighSeverity

	for category, count := range feedback.ByPlanningCategory {
		report.PlanningCategories[category] = count
	}

	report.TopComponents = auditTopCounts(feedback.ByComponent, 6)
	report.RecommendedSurfaces = planningQualityRecommendedSurfaces(report)

	if len(report.PlanningCategories) == 0 {
		report.PlanningCategories = nil
	}

	return report
}

func planningQualityFindings(report *harnessAuditPlanningQuality) []harnessAuditFinding {
	if report == nil || report.PlanningFeedback == 0 {
		return nil
	}

	severity := "medium"
	if report.HighSeverity > 0 {
		severity = "high"
	}

	return []harnessAuditFinding{{
		ID:             "planning_quality_feedback_reported",
		Severity:       severity,
		Evidence:       fmt.Sprintf("%d planning-quality feedback event(s); top planning category %s", report.PlanningFeedback, auditTopCount(report.PlanningCategories)),
		Recommendation: "Triage planning-quality feedback into context impact ranking, packet schema, plan audit, budget, or replay-eval improvements before adding prompt text.",
		Tools:          []string{ToolNameHarnessFeedback, ToolNameHarnessAudit, ToolNameContextImpact, "architect plan-audit"},
	}}
}

func planningQualityQuestions(report *harnessAuditPlanningQuality) []harnessAuditQuestion {
	if report == nil || report.PlanningFeedback == 0 {
		return nil
	}

	return []harnessAuditQuestion{{
		Area:      "planning_quality",
		Question:  "Which planning-quality feedback category should become the next deterministic packet, ranking, audit, or eval contract?",
		Evidence:  fmt.Sprintf("%d planning-quality feedback event(s); recommended surfaces: %s", report.PlanningFeedback, strings.Join(report.RecommendedSurfaces, ", ")),
		Candidate: "Prefer a typed context_impact, planning_v2, plan-audit, budget, or replay-eval improvement over another strategy prompt.",
	}}
}

func planningQualityRecommendedSurfaces(report *harnessAuditPlanningQuality) []string {
	if report == nil {
		return nil
	}

	var surfaces []string

	if report.PlanningCategories["packet_quality"] > 0 || report.PlanningCategories["planning_quality"] > 0 {
		surfaces = append(surfaces, "architect packet --schema planning_v2")
	}

	if report.PlanningCategories["impact_ranking"] > 0 || report.PlanningCategories["missing_affordance"] > 0 || report.PlanningCategories["bad_scope_rank"] > 0 || report.PlanningCategories["read_loop"] > 0 {
		surfaces = append(surfaces, ToolNameContextImpact)
	}

	if report.PlanningCategories["plan_audit_gap"] > 0 || report.PlanningCategories["edit_readiness_failure"] > 0 || report.PlanningCategories["unsupported_plan_claim"] > 0 || report.PlanningCategories["validation_gap"] > 0 {
		surfaces = append(surfaces, "architect plan-audit")
	}

	if report.PlanningCategories["budget_gap"] > 0 || report.PlanningCategories["packet_bloat"] > 0 {
		surfaces = append(surfaces, "architect budget")
	}

	if report.PlanningCategories["replay_eval_gap"] > 0 {
		surfaces = append(surfaces, "architect replay-compare")
	}

	if report.PlanningCategories["stale_fact"] > 0 || report.PlanningCategories["backend_gap"] > 0 || report.PlanningCategories["language_precision_gap"] > 0 {
		surfaces = append(surfaces, ToolNameContextStatus, ToolNameContextPack)
	}

	if len(surfaces) == 0 && report.PlanningFeedback > 0 {
		surfaces = append(surfaces, ToolNameHarnessFeedback)
	}

	return textutil.UniqueNonEmpty(surfaces)
}
