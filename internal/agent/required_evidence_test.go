package agent

import (
	"slices"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestMissingRequiredEvidenceUsesTypedRegistryAndSkipsSemanticLabels(t *testing.T) {
	missing := missingRequiredEvidence([]string{
		" " + string(workerruntime.RequiredEvidenceVerificationStatus) + " ",
		string(workerruntime.RequiredEvidenceConfidence),
	}, "", nil)
	if !slices.Equal(missing, []string{string(workerruntime.RequiredEvidenceVerificationStatus)}) {
		t.Fatalf("missing evidence = %#v, want only deterministic verification status", missing)
	}

	missing = missingRequiredEvidence([]string{
		string(workerruntime.RequiredEvidenceVerificationStatus),
	}, "", []workspace.ToolResult{{Tool: "source_sanity", Summary: "ok"}})
	if len(missing) != 0 {
		t.Fatalf("verification tool result should satisfy verification evidence, got %#v", missing)
	}
}
