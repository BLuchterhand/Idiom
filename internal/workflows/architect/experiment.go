package architect

import (
	"cmp"
	"slices"
	"strings"
)

type BudgetExperiment struct {
	SchemaVersion string                 `json:"schema_version"`
	Tiers         []ContextBudgetReport  `json:"tiers"`
	Placement     []PacketPlacementProbe `json:"placement,omitempty"`
}

type PacketPlacementProbe struct {
	Section         string `json:"section"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Items           int    `json:"items,omitempty"`
	SuggestedRegion string `json:"suggested_region"`
	Rationale       string `json:"rationale,omitempty"`
}

func BuildBudgetExperiment(packet Packet, tiers []string) BudgetExperiment {
	if len(tiers) == 0 {
		tiers = []string{"brief", "standard", "deep", "max-local"}
	}
	out := BudgetExperiment{
		SchemaVersion: packet.SchemaVersion,
	}
	for _, tier := range tiers {
		report := BuildContextBudget(packet, ContextBudgetOptions{Tier: tier})
		if report != nil {
			out.Tiers = append(out.Tiers, *report)
		}
	}
	out.Placement = packetPlacementProbes(packet)
	return out
}

func packetPlacementProbes(packet Packet) []PacketPlacementProbe {
	report := BuildContextBudget(packet, ContextBudgetOptions{Tier: "standard"})
	if report == nil {
		return nil
	}
	var probes []PacketPlacementProbe
	for _, allocation := range report.Allocations {
		region, rationale := suggestedPacketRegion(allocation.Category)
		if region == "" {
			continue
		}
		probes = append(probes, PacketPlacementProbe{
			Section:         allocation.Category,
			EstimatedTokens: allocation.EstimatedTokens,
			Items:           allocation.Items,
			SuggestedRegion: region,
			Rationale:       rationale,
		})
	}
	slices.SortFunc(probes, func(a, b PacketPlacementProbe) int {
		if c := cmp.Compare(packetRegionRank(a.SuggestedRegion), packetRegionRank(b.SuggestedRegion)); c != 0 {
			return c
		}
		return cmp.Compare(a.Section, b.Section)
	})
	return probes
}

func suggestedPacketRegion(category string) (string, string) {
	switch strings.TrimSpace(category) {
	case "user_task", "contracts", "constraints", "validation_commands":
		return "head", "load-bearing task and contract facts should be early"
	case "candidate_scope", "repository_view", "freshness", "language_capabilities", "handoff":
		return "head_or_tail", "planner decisions depend on these facts; avoid burying them in the middle"
	case "evidence_rule", "evidence_context_view", "evidence_collector":
		return "middle", "supporting evidence can sit behind ranked scope when handles remain available"
	case "unknowns", "risks":
		return "tail", "close the packet with unresolved risks so they remain visible before generation"
	default:
		return "", ""
	}
}

func packetRegionRank(region string) int {
	switch region {
	case "head":
		return 0
	case "head_or_tail":
		return 1
	case "middle":
		return 2
	case "tail":
		return 3
	default:
		return 4
	}
}

type PacketVariantComparison struct {
	Task     string                 `json:"task,omitempty"`
	Variants []PacketVariantSummary `json:"variants"`
	Deltas   []string               `json:"deltas,omitempty"`
}

type PacketVariantSummary struct {
	Label                  string `json:"label"`
	SchemaVersion          string `json:"schema_version"`
	EvidenceItems          int    `json:"evidence_items"`
	CandidateScopeItems    int    `json:"candidate_scope_items,omitempty"`
	ValidationRequirements int    `json:"validation_requirements,omitempty"`
	ContextBudgetRisk      string `json:"context_budget_risk,omitempty"`
	EstimatedTokens        int    `json:"estimated_tokens,omitempty"`
	Freshness              string `json:"freshness,omitempty"`
}

func ComparePacketVariants(base Packet, candidate Packet) PacketVariantComparison {
	comparison := PacketVariantComparison{
		Task: firstNonEmpty(base.Task, candidate.Task),
		Variants: []PacketVariantSummary{
			packetVariantSummary("base", base),
			packetVariantSummary("candidate", candidate),
		},
	}
	comparison.Deltas = packetVariantDeltas(base, candidate)
	return comparison
}

func packetVariantSummary(label string, packet Packet) PacketVariantSummary {
	summary := PacketVariantSummary{
		Label:         label,
		SchemaVersion: packet.SchemaVersion,
		EvidenceItems: len(packet.Evidence),
	}
	if packet.Contracts != nil {
		summary.ValidationRequirements = len(packet.Contracts.ValidationRequirements)
	} else {
		summary.ValidationRequirements = len(packet.Tests)
	}
	summary.CandidateScopeItems = len(packet.CandidateScope)
	if packet.ContextBudget != nil {
		summary.ContextBudgetRisk = packet.ContextBudget.Risk
		summary.EstimatedTokens = packet.ContextBudget.EstimatedTokens
	}
	if packet.Freshness != nil && packet.Freshness.Fresh != nil {
		if *packet.Freshness.Fresh {
			summary.Freshness = "fresh"
		} else {
			summary.Freshness = "stale"
		}
	}
	return summary
}

func packetVariantDeltas(base Packet, candidate Packet) []string {
	var deltas []string
	if len(candidate.CandidateScope) > len(base.CandidateScope) {
		deltas = append(deltas, "candidate adds ranked candidate_scope")
	}
	baseValidation := len(base.Tests)
	if base.Contracts != nil {
		baseValidation = len(base.Contracts.ValidationRequirements)
	}
	candidateValidation := len(candidate.Tests)
	if candidate.Contracts != nil {
		candidateValidation = len(candidate.Contracts.ValidationRequirements)
	}
	if candidateValidation > baseValidation {
		deltas = append(deltas, "candidate carries more validation requirements")
	}
	if candidate.ContextBudget != nil && base.ContextBudget == nil {
		deltas = append(deltas, "candidate adds context budget reporting")
	}
	if candidate.Freshness != nil && base.Freshness == nil {
		deltas = append(deltas, "candidate adds freshness reporting")
	}
	if len(candidate.LanguageCapabilities) > len(base.LanguageCapabilities) {
		deltas = append(deltas, "candidate adds language capability precision tiers")
	}
	return deltas
}

type PacketReplayComparison struct {
	Task              string                    `json:"task,omitempty"`
	PacketComparison  PacketVariantComparison   `json:"packet_comparison"`
	PlanAudits        []PlanVariantAuditSummary `json:"plan_audits"`
	ReplayDeltas      []string                  `json:"replay_deltas,omitempty"`
	PreferredVariant  string                    `json:"preferred_variant,omitempty"`
	ComparisonCaveats []string                  `json:"comparison_caveats,omitempty"`
}

type PlanVariantAuditSummary struct {
	Label           string                   `json:"label"`
	OK              bool                     `json:"ok"`
	Errors          int                      `json:"errors,omitempty"`
	Warnings        int                      `json:"warnings,omitempty"`
	Findings        int                      `json:"findings,omitempty"`
	FindingsByKind  map[string]int           `json:"findings_by_kind,omitempty"`
	MismatchMetrics PlanAuditMismatchMetrics `json:"mismatch_metrics"`
	AuditFindings   []PlanAuditFinding       `json:"audit_findings,omitempty"`
}

func ComparePacketReplay(base Packet, candidate Packet, basePlan string, candidatePlan string) PacketReplayComparison {
	baseAudit := planVariantAuditSummary("base", base, basePlan)
	candidateAudit := planVariantAuditSummary("candidate", candidate, candidatePlan)
	out := PacketReplayComparison{
		Task:             firstNonEmpty(base.Task, candidate.Task),
		PacketComparison: ComparePacketVariants(base, candidate),
		PlanAudits:       []PlanVariantAuditSummary{baseAudit, candidateAudit},
	}
	out.ReplayDeltas = planAuditDeltas(baseAudit, candidateAudit)
	out.PreferredVariant = preferredPlanVariant(baseAudit, candidateAudit)
	if strings.TrimSpace(base.Task) != "" && strings.TrimSpace(candidate.Task) != "" && strings.TrimSpace(base.Task) != strings.TrimSpace(candidate.Task) {
		out.ComparisonCaveats = append(out.ComparisonCaveats, "base and candidate packets have different task text; replay comparison is not same-task")
	}
	return out
}

func planVariantAuditSummary(label string, packet Packet, plan string) PlanVariantAuditSummary {
	result := AuditPlan(packet, plan)
	summary := PlanVariantAuditSummary{
		Label:           label,
		OK:              result.OK,
		Findings:        len(result.Findings),
		FindingsByKind:  make(map[string]int),
		MismatchMetrics: result.MismatchMetrics,
		AuditFindings:   append([]PlanAuditFinding(nil), result.Findings...),
	}
	for _, finding := range result.Findings {
		switch finding.Severity {
		case "error":
			summary.Errors++
		case "warning":
			summary.Warnings++
		}
		if finding.Kind != "" {
			summary.FindingsByKind[finding.Kind]++
		}
	}
	if len(summary.FindingsByKind) == 0 {
		summary.FindingsByKind = nil
	}
	return summary
}

func planAuditDeltas(base PlanVariantAuditSummary, candidate PlanVariantAuditSummary) []string {
	var deltas []string
	if totalPacketScopeMismatches(candidate.MismatchMetrics) < totalPacketScopeMismatches(base.MismatchMetrics) {
		deltas = append(deltas, "candidate plan has fewer packet/scope mismatch signals")
	}
	if candidate.Errors < base.Errors {
		deltas = append(deltas, "candidate plan has fewer audit errors")
	}
	if candidate.Warnings < base.Warnings {
		deltas = append(deltas, "candidate plan has fewer audit warnings")
	}
	if candidate.Findings < base.Findings {
		deltas = append(deltas, "candidate plan has fewer total audit findings")
	}
	if base.OK != candidate.OK {
		if candidate.OK {
			deltas = append(deltas, "candidate plan passes deterministic audit while base does not")
		} else {
			deltas = append(deltas, "candidate plan fails deterministic audit while base passes")
		}
	}
	return deltas
}

func totalPacketScopeMismatches(metrics PlanAuditMismatchMetrics) int {
	return metrics.PacketIdentityMismatches +
		metrics.PacketFreshnessMismatches +
		metrics.ScopePathMismatches +
		metrics.EvidenceMismatches +
		metrics.UnresolvedQuestionMismatches
}

func preferredPlanVariant(base PlanVariantAuditSummary, candidate PlanVariantAuditSummary) string {
	baseScore := base.Errors*100 + base.Warnings*10 + base.Findings
	candidateScore := candidate.Errors*100 + candidate.Warnings*10 + candidate.Findings
	switch {
	case candidateScore < baseScore:
		return "candidate"
	case baseScore < candidateScore:
		return "base"
	default:
		return "tie"
	}
}
