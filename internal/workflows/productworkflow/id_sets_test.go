package productworkflow

import "testing"

func TestProductWorkflowIDSetsTrimAndSkipBlankIDs(t *testing.T) {
	evidence := EvidenceCardIDSet([]EvidenceCard{{CardID: " EV-1 "}, {CardID: ""}})
	if !evidence["EV-1"] || len(evidence) != 1 {
		t.Fatalf("unexpected evidence IDs: %#v", evidence)
	}

	requirements := PRDRequirementIDSet([]PRDRequirement{{ID: " REQ-1 "}, {ID: ""}})
	if !requirements["REQ-1"] || len(requirements) != 1 {
		t.Fatalf("unexpected requirement IDs: %#v", requirements)
	}

	adrs := ADRIndexIDSet(ADRIndex{ADRs: []ADRIndexEntry{{ADRID: " ADR-1 "}, {ADRID: ""}}})
	if !adrs["ADR-1"] || len(adrs) != 1 {
		t.Fatalf("unexpected ADR IDs: %#v", adrs)
	}
}
