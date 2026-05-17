package writerroute

import (
	"encoding/json"
	"testing"
)

// TestWriterRouteDecision_NormalizationAndStableID verifies string normalization,
// stable RouteID computation across slice permutations, and schema version.
func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {
	d1 := NewDecision(ModePlanSubmission, "plan-execute", "", "", "", "", "", "", "", "", "", []string{" a.go ", "b.go"}, []string{"e1", "e2"}, []string{"r1", "r2"}, []string{"eT1"}, false, "family")
	d2 := NewDecision(ModePlanSubmission, "plan-execute", "", "", "", "", "", "", "", "", "", []string{"b.go", "A.go"}, []string{"e2", "e1"}, []string{"r2", "r1"}, []string{"et1"}, false, "family")

	if d1.RouteID != d2.RouteID {
		t.Errorf("RouteID mismatch: d1=%s, d2=%s; expected equal IDs for same content", d1.RouteID, d2.RouteID)
	}
	if d1.SchemaVersion != SchemaVersion {
		t.Errorf("unexpected schema version: %s", d1.SchemaVersion)
	}
	if d1.RouteID == "" {
		t.Error("RouteID must not be empty")
	}
	if len(d1.RequiredEditPaths) != 2 || d1.RequiredEditPaths[0] != "a.go" || d1.RequiredEditPaths[1] != "b.go" {
		t.Fatalf("required paths were not normalized and sorted: %#v", d1.RequiredEditPaths)
	}
	if d1.CostTier != "local_first" || d1.ReviewPolicy != "local_first" || d1.ScopeTier != "single_atom" {
		t.Fatalf("defaults not applied: %#v", d1)
	}
}

// TestWriterRouteDecision_EventFields verifies the EventFields mapping and defaults.
func TestWriterRouteDecision_EventFields(t *testing.T) {
	d := NewDecisionFromParams(DecisionParams{
		Mode:              ModePlanSubmission,
		Lane:              "plan-execute",
		PlannerModel:      "planner",
		ExecutorModel:     "executor",
		ReviewerModel:     "reviewer",
		ClassifierModel:   "classifier",
		RoleAliases:       map[string]string{" Planner ": "planner", "executor": "executor"},
		RiskTier:          "low",
		VerificationOwner: "planner_owned",
		RequiredEditPaths: []string{"path.go"},
		EvidencePacketIDs: []string{"ev1"},
		ReasonCodes:       []string{"review"},
		EscalationTriggers: []string{
			"auto",
		},
		TaskFamily: "plan_then_execute",
	})

	fields := d.EventFields()
	if len(fields) == 0 {
		t.Fatal("EventFields must return non-empty map")
	}

	// Verify specific field presence and values based on spec assumptions.
	if fields["schema_version"] != "idiom.writer_route.v1" {
		t.Errorf("expected schema_version field, got %v", fields["schema_version"])
	}
	if fields["cost_tier"] == "" || fields["review_policy"] == "" {
		t.Errorf("expected default cost_tier and review_policy fields, got %v", fields)
	}
	if fields["route_id"] != d.RouteID {
		t.Errorf("route_id = %v, want %v", fields["route_id"], d.RouteID)
	}
	aliases, ok := fields["role_aliases"].(map[string]string)
	if !ok || aliases["planner"] != "planner" || aliases["executor"] != "executor" {
		t.Fatalf("role aliases not normalized into event fields: %#v", fields["role_aliases"])
	}

	// Verify JSON marshaling consistency.
	jsonBytes, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("EventFields must marshal to valid JSON: %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Fatal("marshaled JSON must not be empty")
	}
}
