package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/repository/comprehension"
)

const (
	rankerWeightsRelativePath  = ".idiom/ranker/weights.json"
	rankerLearningRelativePath = ".idiom/ranker/learning_log.jsonl"
)

type harnessAuditRankerLearningStatus string

// harnessAuditRankerLearning surfaces the state of the per-workspace
// learnable ranker weights and the learning log so audits can recommend
// where to wire additional trigger sites. This is the "audit checking" layer
// over the eval and CLI triggers; future automated recommendations read
// here.
type harnessAuditRankerLearning struct {
	Status                 harnessAuditRankerLearningStatus   `json:"status"`
	ActionRequired         bool                               `json:"action_required"`
	StatusDetail           string                             `json:"status_detail"`
	Generation             string                             `json:"generation,omitempty"`
	IntegrityIssueCount    int                                `json:"integrity_issue_count"`
	IntegrityIssues        []harnessAuditRankerIntegrityIssue `json:"integrity_issues,omitempty"`
	WeightsPath            string                             `json:"weights_path"`
	WeightsFileExists      bool                               `json:"weights_file_exists"`
	WeightCount            int                                `json:"weight_count"`
	MaxMultiplier          float64                            `json:"max_multiplier,omitempty"`
	MinMultiplier          float64                            `json:"min_multiplier,omitempty"`
	LearningLogPath        string                             `json:"learning_log_path"`
	LearningLogExists      bool                               `json:"learning_log_exists"`
	LearningSummary        *comprehension.LearningLogSummary  `json:"learning_summary,omitempty"`
	WiredSources           []string                           `json:"wired_sources,omitempty"`
	DormantSources         []string                           `json:"dormant_sources,omitempty"`
	TriggerRecommendations []harnessAuditRankerTrigger        `json:"trigger_recommendations,omitempty"`
}

type harnessAuditRankerIntegrityIssue struct {
	Artifact string `json:"artifact"`
	Kind     string `json:"kind"`
	Count    int    `json:"count,omitempty"`
	Detail   string `json:"detail"`
}

const (
	rankerLearningStatusIdleExpected    harnessAuditRankerLearningStatus = "idle_expected"
	rankerLearningStatusInitializedIdle harnessAuditRankerLearningStatus = "initialized_idle"
	rankerLearningStatusActive          harnessAuditRankerLearningStatus = "active"
	rankerLearningStatusDegraded        harnessAuditRankerLearningStatus = "degraded"
)

// harnessAuditRankerTrigger describes one candidate trigger site for the
// learning loop. The audit lists every known candidate, marks whether it has
// been observed in the learning log, and explains how it would be wired.
type harnessAuditRankerTrigger struct {
	Name        string `json:"name"`
	Status      string `json:"status"` // wired | dormant | partial
	Component   string `json:"component,omitempty"`
	Description string `json:"description"`
	Suggested   string `json:"suggested,omitempty"`
}

// rankerLearningKnownTriggers is the canonical list of trigger sites the
// audit knows about. Each entry has a stable name, the rough component that
// would host it, and a one-line description so future automated
// recommendations have something to point at.
var rankerLearningKnownTriggers = []harnessAuditRankerTrigger{
	{
		Name:        "eval",
		Component:   "internal/evidence/eval/swe_suite.go",
		Description: "SWE eval groundedness oracle boost/penalize bands",
	},
	{
		Name:        "cli",
		Component:   "internal/surfaces/cli/ranker/rankercmd.go",
		Description: "manual `idiom ranker boost|penalize|reset` invocations",
	},
	{
		Name:        "verified_edit",
		Component:   "internal/runtime/agent/runner.go",
		Description: "boost files surfaced before a verified edit; not yet wired",
	},
	{
		Name:        "harness_feedback",
		Component:   "internal/capabilities/workspace/harness_feedback.go",
		Description: "self-reported relevance feedback from the agent; not yet wired",
	},
	{
		Name:        "audit_watch",
		Component:   "internal/surfaces/cli/audit/audit.go",
		Description: "autonomous review packets folding learning updates; not yet wired",
	},
	{
		Name:        comprehension.LearningSourceRecoveryReconciliation,
		Component:   "internal/surfaces/cli/recover/packet.go",
		Description: "verified recovery reconciliation outcomes recorded as evidence-only learning events",
	},
}

// buildRankerLearningAudit reads the persisted ranker weights and learning
// log and returns the audit projection used by harness_audit.
func buildRankerLearningAudit(ctx context.Context, root string) *harnessAuditRankerLearning {
	out := &harnessAuditRankerLearning{
		WeightsPath:     filepath.Join(root, filepath.FromSlash(rankerWeightsRelativePath)),
		LearningLogPath: filepath.Join(root, filepath.FromSlash(rankerLearningRelativePath)),
	}

	snapshot, err := comprehension.LoadRankerStateSnapshot(ctx, root)
	if err != nil {
		out.IntegrityIssues = append(out.IntegrityIssues, harnessAuditRankerIntegrityIssue{
			Artifact: "ranker_state",
			Kind:     "unreadable",
			Count:    1,
			Detail:   "ranker state owner rejected the lock-consistent snapshot",
		})
		out.IntegrityIssueCount = len(out.IntegrityIssues)
		classifyRankerLearningStatus(out)

		return out
	}

	out.Generation = snapshot.Generation
	out.WeightsFileExists = snapshot.WeightsMaterialized
	out.LearningLogExists = snapshot.LearningLogPublished
	setRankerWeightSummary(out, snapshot.LearnedWeights())
	if snapshot.LearningLogPublished {
		summary := snapshot.AuditSummary()
		out.LearningSummary = &summary
	}
	out.WiredSources, out.DormantSources, out.TriggerRecommendations = classifyRankerTriggers(out.LearningSummary)

	classifyRankerLearningStatus(out)

	return out
}

func setRankerWeightSummary(out *harnessAuditRankerLearning, weights map[string]float64) {
	if out == nil {
		return
	}

	out.WeightCount = len(weights)
	if out.WeightCount == 0 {
		return
	}

	first := true
	for _, multiplier := range weights {
		if first {
			out.MinMultiplier = multiplier
			out.MaxMultiplier = multiplier
			first = false

			continue
		}

		if multiplier < out.MinMultiplier {
			out.MinMultiplier = multiplier
		}
		if multiplier > out.MaxMultiplier {
			out.MaxMultiplier = multiplier
		}
	}
}

func classifyRankerLearningStatus(learning *harnessAuditRankerLearning) {
	if learning == nil {
		return
	}

	switch {
	case learning.IntegrityIssueCount > 0:
		learning.Status = rankerLearningStatusDegraded
		learning.ActionRequired = true
		learning.StatusDetail = fmt.Sprintf("%d persisted ranker integrity issue(s) require attention", learning.IntegrityIssueCount)
	case learningSummaryMostlyMutableNoop(learning.LearningSummary):
		learning.Status = rankerLearningStatusDegraded
		learning.ActionRequired = true
		learning.StatusDetail = "observed mutable learning events are predominantly no-op"
	case learning.WeightCount > 0 || learning.LearningSummary != nil:
		learning.Status = rankerLearningStatusActive
		learning.StatusDetail = "ranker state contains observed weights or learning events"
	case learning.WeightsFileExists || learning.LearningLogExists:
		learning.Status = rankerLearningStatusInitializedIdle
		learning.StatusDetail = "ranker state files exist but contain no learned weights or events; no action is required unless learning was expected"
	default:
		learning.Status = rankerLearningStatusIdleExpected
		learning.StatusDetail = "ranker learning is opt-in and no local ranker state has been created"
	}
}

// classifyRankerTriggers labels every known trigger as wired (observed in
// the log), partial (some signal but missing for >30 days), or dormant
// (never observed). The harness audit uses this to surface concrete
// next-step suggestions for the learning loop.
func classifyRankerTriggers(summary *comprehension.LearningLogSummary) (wired, dormant []string, triggers []harnessAuditRankerTrigger) {
	bySource := map[string]int{}
	if summary != nil {
		bySource = summary.BySource
	}

	for _, base := range rankerLearningKnownTriggers {
		t := base
		count := bySource[t.Name]

		switch {
		case count > 0:
			t.Status = "wired"
			wired = append(wired, t.Name)
		default:
			t.Status = "dormant"
			dormant = append(dormant, t.Name)
		}

		t.Suggested = rankerTriggerSuggestion(t.Name, t.Status, count)
		triggers = append(triggers, t)
	}

	return wired, dormant, triggers
}

func rankerTriggerSuggestion(name, status string, count int) string {
	if status == "wired" {
		return fmt.Sprintf("observed %d entry(ies) from %s; keep monitoring decay and weight drift", count, name)
	}

	switch name {
	case "eval":
		return "SWE oracle Groundedness coverage on at least one task would produce eval-driven boost/penalize signals"
	case "cli":
		return "manual ranker CLI signals can seed boost/penalize evidence or override automated decisions"
	case "verified_edit":
		return "verified-edit signals can cover files that appeared in recent context packets after verification clears a write"
	case "harness_feedback":
		return "harness_feedback relevance categories can feed learned weights from self-reported useful and misleading files"
	case "audit_watch":
		return "audit watch review packets can convert low-groundedness session summaries and surfaced files into learned-weight evidence"
	case comprehension.LearningSourceRecoveryReconciliation:
		return "successful `idiom recover reconcile` runs record verified recovery outcomes without mutating learned weights"
	default:
		return "no signal observed yet"
	}
}

func rankerLearningFindings(learning *harnessAuditRankerLearning) []harnessAuditFinding {
	if learning == nil {
		return nil
	}

	var findings []harnessAuditFinding
	if learning.IntegrityIssueCount > 0 {
		return []harnessAuditFinding{{
			ID:             "ranker_learning_state_integrity",
			Severity:       "medium",
			Evidence:       rankerLearningIntegrityEvidence(learning.IntegrityIssues),
			Recommendation: "Repair or quarantine the named .idiom/ranker artifact, then rerun harness_audit; use ranker reset only for an intentional valid weight reset.",
		}}
	}

	if learning.IntegrityIssueCount == 0 && learning.Status == "" && !learning.WeightsFileExists && !learning.LearningLogExists {
		findings = append(findings, harnessAuditFinding{
			ID:             "ranker_learning_idle",
			Severity:       "info",
			Evidence:       "ranker learning is idle: no .idiom/ranker/weights.json or learning_log.jsonl present; expected before an eval, ranker CLI command, or recovery reconciliation records evidence",
			Recommendation: "no action needed unless learned ranker state was expected; run or wire an observed trigger source before expecting weight signals",
		})

		return findings
	}

	if learningSummaryMostlyMutableNoop(learning.LearningSummary) {
		findings = append(findings, harnessAuditFinding{
			ID:             "ranker_learning_mostly_noop",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d of %d learning entries landed in the no-op band", learning.LearningSummary.NoOpCount, learning.LearningSummary.TotalEntries),
			Recommendation: "raise oracle MinScore so high-quality runs cross the 0.75 boost threshold, or supply richer Groundedness ContextFiles",
		})
	}

	return findings
}

func rankerLearningIntegrityEvidence(issues []harnessAuditRankerIntegrityIssue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		artifact := strings.TrimSpace(issue.Artifact)
		kind := strings.TrimSpace(issue.Kind)
		detail := strings.TrimSpace(issue.Detail)
		if artifact == "" || kind == "" || detail == "" {
			continue
		}

		parts = append(parts, artifact+" "+kind+": "+detail)
	}
	if len(parts) == 0 {
		return "persisted ranker state failed integrity validation"
	}

	return strings.Join(parts, "; ")
}

func learningSummaryMostlyMutableNoop(summary *comprehension.LearningLogSummary) bool {
	if summary == nil || summary.TotalEntries == 0 {
		return false
	}

	evidenceOnly := 0
	if summary.BySource != nil {
		evidenceOnly += summary.BySource[comprehension.LearningSourceRecoveryReconciliation]
	}

	mutableEntries := summary.TotalEntries - evidenceOnly
	if mutableEntries <= 0 {
		return false
	}

	mutableNoOps := summary.NoOpCount - evidenceOnly
	if mutableNoOps < 0 {
		mutableNoOps = 0
	}

	return mutableNoOps*4 > mutableEntries*3
}

func rankerLearningQuestions(learning *harnessAuditRankerLearning) []harnessAuditQuestion {
	if learning == nil || learning.IntegrityIssueCount > 0 {
		return nil
	}

	var questions []harnessAuditQuestion

	for _, t := range learning.TriggerRecommendations {
		if t.Status == "wired" {
			continue
		}

		questions = append(questions, harnessAuditQuestion{
			Area:      "ranker_learning",
			Question:  fmt.Sprintf("Should the %s trigger be wired to feed the learning loop?", t.Name),
			Evidence:  t.Description,
			Candidate: t.Component,
		})
	}

	return questions
}

func rankerLearningSuggestsChanges(learning *harnessAuditRankerLearning) bool {
	if learning == nil {
		return false
	}
	if learning.IntegrityIssueCount > 0 {
		return false
	}

	return learning.Status != rankerLearningStatusIdleExpected && learning.Status != rankerLearningStatusInitializedIdle
}
