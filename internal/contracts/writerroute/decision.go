package writerroute

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion is the current version of the WriterRouteDecision schema.
const SchemaVersion = "idiom.writer_route.v1"

// WriterMode represents the routing mode for a decision.
type WriterMode string

const (
	ModeAgenticGuarded             WriterMode = "agentic_guarded"
	ModePlanSubmission             WriterMode = "plan_submission"
	ModeDeterministicArtifactApply WriterMode = "deterministic_artifact_apply"
	ModeHostMaterializedArtifact   WriterMode = "host_materialized_artifact"
	ModeReadOnly                   WriterMode = "read_only"
)

// Decision represents a passive writer route decision.
type Decision struct {
	SchemaVersion      string            `json:"schema_version"`
	RouteID            string            `json:"route_id"`
	TaskFamily         string            `json:"task_family,omitempty"`
	WriterMode         string            `json:"writer_mode"`
	Lane               string            `json:"lane,omitempty"`
	PlannerModel       string            `json:"planner_model,omitempty"`
	ExecutorModel      string            `json:"executor_model,omitempty"`
	ReviewerModel      string            `json:"reviewer_model,omitempty"`
	ClassifierModel    string            `json:"classifier_model,omitempty"`
	EffectiveModel     string            `json:"effective_model,omitempty"`
	RoleAliases        map[string]string `json:"role_aliases,omitempty"`
	RiskTier           string            `json:"risk_tier,omitempty"`
	CostTier           string            `json:"cost_tier,omitempty"`
	ScopeTier          string            `json:"scope_tier,omitempty"`
	VerificationOwner  string            `json:"verification_owner,omitempty"`
	ReviewPolicy       string            `json:"review_policy,omitempty"`
	RequiredEditPaths  []string          `json:"required_edit_paths,omitempty"`
	EvidencePacketIDs  []string          `json:"evidence_packet_ids,omitempty"`
	ReasonCodes        []string          `json:"reason_codes,omitempty"`
	EscalationTriggers []string          `json:"escalation_triggers,omitempty"`
	UserOverride       bool              `json:"user_override,omitempty"`
}

type DecisionParams struct {
	Mode               WriterMode
	Lane               string
	PlannerModel       string
	ExecutorModel      string
	ReviewerModel      string
	ClassifierModel    string
	EffectiveModel     string
	RoleAliases        map[string]string
	RiskTier           string
	CostTier           string
	ScopeTier          string
	VerificationOwner  string
	ReviewPolicy       string
	RequiredEditPaths  []string
	EvidencePacketIDs  []string
	ReasonCodes        []string
	EscalationTriggers []string
	UserOverride       bool
	TaskFamily         string
}

// EffectiveModelForMode selects the model that owns generation for a writer
// mode after role aliases and runtime config have already been resolved.
func EffectiveModelForMode(mode WriterMode, plannerModel, executorModel, classifierModel string) string {
	switch mode {
	case ModeAgenticGuarded, ModeDeterministicArtifactApply, ModeHostMaterializedArtifact:
		return strings.TrimSpace(executorModel)
	case ModePlanSubmission:
		return strings.TrimSpace(plannerModel)
	case ModeReadOnly:
		if model := strings.TrimSpace(classifierModel); model != "" {
			return model
		}

		return strings.TrimSpace(plannerModel)
	default:
		if model := strings.TrimSpace(executorModel); model != "" {
			return model
		}

		return strings.TrimSpace(plannerModel)
	}
}

// NewDecisionFromParams creates a normalized report-only writer route decision.
func NewDecisionFromParams(params DecisionParams) Decision {
	normalizedRequiredEditPaths := normalizeAndSort(params.RequiredEditPaths)
	costTier := strings.TrimSpace(params.CostTier)

	if costTier == "" {
		costTier = "local_first"
	}

	reviewPolicy := strings.TrimSpace(params.ReviewPolicy)
	if reviewPolicy == "" {
		reviewPolicy = "local_first"
	}

	scopeTier := strings.TrimSpace(params.ScopeTier)
	if scopeTier == "" && len(normalizedRequiredEditPaths) > 0 {
		scopeTier = "single_atom"
	}

	d := Decision{
		SchemaVersion:      SchemaVersion,
		WriterMode:         string(params.Mode),
		Lane:               strings.TrimSpace(params.Lane),
		PlannerModel:       strings.TrimSpace(params.PlannerModel),
		ExecutorModel:      strings.TrimSpace(params.ExecutorModel),
		ReviewerModel:      strings.TrimSpace(params.ReviewerModel),
		ClassifierModel:    strings.TrimSpace(params.ClassifierModel),
		EffectiveModel:     strings.TrimSpace(params.EffectiveModel),
		RoleAliases:        normalizeStringMap(params.RoleAliases),
		RiskTier:           strings.TrimSpace(params.RiskTier),
		CostTier:           costTier,
		ScopeTier:          scopeTier,
		VerificationOwner:  strings.TrimSpace(params.VerificationOwner),
		ReviewPolicy:       reviewPolicy,
		RequiredEditPaths:  normalizedRequiredEditPaths,
		EvidencePacketIDs:  normalizeAndSort(params.EvidencePacketIDs),
		ReasonCodes:        normalizeAndSort(params.ReasonCodes),
		EscalationTriggers: normalizeAndSort(params.EscalationTriggers),
		UserOverride:       params.UserOverride,
		TaskFamily:         strings.TrimSpace(params.TaskFamily),
	}

	d.RouteID = d.computeRouteID()

	return d
}

// normalizeAndSort normalizes strings and sorts set-like slices.
func normalizeAndSort(items []string) []string {
	if items == nil {
		return nil
	}

	seen := make(map[string]bool)

	var unique []string

	for _, item := range items {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized != "" && !seen[normalized] {
			seen[normalized] = true
			unique = append(unique, normalized)
		}
	}

	sort.Strings(unique)

	return unique
}

func normalizeStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}

	out := make(map[string]string, len(items))
	for key, value := range items {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		if key == "" || value == "" {
			continue
		}

		out[key] = value
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// computeRouteID computes a stable hash of the decision, excluding RouteID itself.
func (d *Decision) computeRouteID() string {
	payload := *d

	payload.RouteID = ""

	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", payload))
	}

	hash := sha256.Sum256(data)

	return fmt.Sprintf("%x", hash[:8])
}

// EventFields returns a map of field names to values suitable for event logging.
func (d *Decision) EventFields() map[string]interface{} {
	return map[string]interface{}{
		"schema_version":      d.SchemaVersion,
		"route_id":            d.RouteID,
		"task_family":         d.TaskFamily,
		"writer_mode":         d.WriterMode,
		"lane":                d.Lane,
		"planner_model":       d.PlannerModel,
		"executor_model":      d.ExecutorModel,
		"reviewer_model":      d.ReviewerModel,
		"classifier_model":    d.ClassifierModel,
		"effective_model":     d.EffectiveModel,
		"role_aliases":        d.RoleAliases,
		"risk_tier":           d.RiskTier,
		"cost_tier":           d.CostTier,
		"scope_tier":          d.ScopeTier,
		"verification_owner":  d.VerificationOwner,
		"review_policy":       d.ReviewPolicy,
		"required_edit_paths": d.RequiredEditPaths,
		"evidence_packet_ids": d.EvidencePacketIDs,
		"reason_codes":        d.ReasonCodes,
		"escalation_triggers": d.EscalationTriggers,
		"user_override":       d.UserOverride,
	}
}
