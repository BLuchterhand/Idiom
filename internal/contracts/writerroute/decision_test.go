package writerroute

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {
	d1 := NewDecisionFromParams(DecisionParams{
		Mode:               ModeAgenticGuarded,
		Lane:               " implement ",
		RequiredEditPaths:  []string{" internal/contracts/writerroute/decision.go ", "internal/contracts/writerroute/decision_test.go"},
		EvidencePacketIDs:  []string{"pkt-2", " pkt-1 "},
		ReasonCodes:        []string{"route.single_file_guarded_write", " route.local_model_default "},
		EscalationTriggers: []string{"hosted_review", " stale_anchor "},
		TaskFamily:         " artifact_final_answer ",
	})
	d2 := NewDecisionFromParams(DecisionParams{
		Mode:               ModeAgenticGuarded,
		Lane:               "implement",
		RequiredEditPaths:  []string{"internal/contracts/writerroute/decision_test.go", "internal/contracts/writerroute/decision.go"},
		EvidencePacketIDs:  []string{"pkt-1", "pkt-2"},
		ReasonCodes:        []string{"route.local_model_default", "route.single_file_guarded_write"},
		EscalationTriggers: []string{"stale_anchor", "hosted_review"},
		TaskFamily:         "artifact_final_answer",
	})

	if d1.RouteID == "" {
		t.Fatal("RouteID must be computed")
	}

	if d1.RouteID != d2.RouteID {
		t.Fatalf("RouteID should be stable across set-like slice order: got %q vs %q", d1.RouteID, d2.RouteID)
	}

	wantPaths := []string{"internal/contracts/writerroute/decision.go", "internal/contracts/writerroute/decision_test.go"}
	if !reflect.DeepEqual(d1.RequiredEditPaths, wantPaths) {
		t.Fatalf("RequiredEditPaths normalized = %#v, want %#v", d1.RequiredEditPaths, wantPaths)
	}

	wantReasons := []string{"route.local_model_default", "route.single_file_guarded_write"}
	if !reflect.DeepEqual(d1.ReasonCodes, wantReasons) {
		t.Fatalf("ReasonCodes normalized = %#v, want %#v", d1.ReasonCodes, wantReasons)
	}

	d3 := NewDecisionFromParams(DecisionParams{Mode: ModePlanSubmission, Lane: "implement", TaskFamily: "artifact_final_answer"})
	if d1.RouteID == d3.RouteID {
		t.Fatal("different writer modes should produce different RouteIDs")
	}
}

func TestWriterRouteDecision_EventFields(t *testing.T) {
	d := NewDecisionFromParams(DecisionParams{
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
		VerificationOwner:  "planner_owned",
		RequiredEditPaths:  []string{"b.go", "a.go"},
		EvidencePacketIDs:  []string{"pkt-1"},
		ReasonCodes:        []string{"route.multi_file_plan_execute"},
		EscalationTriggers: []string{"hosted_review"},
		UserOverride:       true,
		TaskFamily:         "plan_then_execute_planner",
	})

	fields := d.EventFields()
	expected := map[string]interface{}{
		"schema_version":      SchemaVersion,
		"route_id":            d.RouteID,
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
	}

	for key, want := range expected {
		if got, ok := fields[key]; !ok {
			t.Fatalf("EventFields missing key %q", key)
		} else if !reflect.DeepEqual(got, want) {
			t.Fatalf("EventFields[%q] = %#v, want %#v", key, got, want)
		}
	}
}

func TestWriterRouteDecision_Defaults(t *testing.T) {
	d := NewDecisionFromParams(DecisionParams{
		Mode:              ModeAgenticGuarded,
		Lane:              "implement",
		RequiredEditPaths: []string{"main.go"},
	})

	if d.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", d.SchemaVersion, SchemaVersion)
	}

	if d.CostTier != "local_first" {
		t.Fatalf("CostTier = %q, want local_first", d.CostTier)
	}

	if d.ReviewPolicy != "local_first" {
		t.Fatalf("ReviewPolicy = %q, want local_first", d.ReviewPolicy)
	}

	if d.ScopeTier != "single_atom" {
		t.Fatalf("ScopeTier = %q, want single_atom when required edit paths exist", d.ScopeTier)
	}

	if d.RouteID == "" {
		t.Fatal("RouteID must be computed")
	}
}

func TestWriterRouteDecision_ModeConstants(t *testing.T) {
	expectedModes := map[WriterMode]string{
		ModeAgenticGuarded:             "agentic_guarded",
		ModePlanSubmission:             "plan_submission",
		ModeDeterministicArtifactApply: "deterministic_artifact_apply",
		ModeHostMaterializedArtifact:   "host_materialized_artifact",
		ModeReadOnly:                   "read_only",
	}

	for mode, want := range expectedModes {
		if string(mode) != want {
			t.Errorf("WriterMode(%q) = %q, want %q", mode, string(mode), want)
		}
	}
}

func TestWriterRouteDecision_JSONEventFieldMapping(t *testing.T) {
	d := NewDecisionFromParams(DecisionParams{
		Mode:               ModeAgenticGuarded,
		Lane:               "implement",
		PlannerModel:       "qwen-2.5-72b",
		ExecutorModel:      "qwen-2.5-72b",
		CostTier:           "local_first",
		ReviewPolicy:       "local_first",
		ScopeTier:          "single_atom",
		VerificationOwner:  "executor",
		RequiredEditPaths:  []string{"main.go"},
		EvidencePacketIDs:  []string{"pkt-1"},
		ReasonCodes:        []string{"route.single_file_guarded_write"},
		EscalationTriggers: []string{"stale_anchor"},
		UserOverride:       true,
		TaskFamily:         "artifact_final_answer",
	})

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var jsonMap map[string]interface{}

	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	fields := d.EventFields()
	for key := range jsonMap {
		if _, ok := fields[key]; !ok {
			t.Errorf("EventFields missing key %q that is present in JSON", key)
		}
	}

	for key, val := range fields {
		if isEmptyEventField(val) {
			continue
		}

		if _, ok := jsonMap[key]; !ok {
			t.Errorf("non-empty EventFields key %q not found in JSON", key)
		}
	}
}

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
