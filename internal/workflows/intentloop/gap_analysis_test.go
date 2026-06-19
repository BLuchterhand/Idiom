package intentloop

import "testing"

func TestGapFingerprintUsesFullDetailHash(t *testing.T) {
	truncated := "same first 2000 bytes"
	gapA := EvidenceGap{
		Requirement: "verification:go test ./...",
		Status:      "failed",
		Detail:      truncated,
		DetailHash:  sha256Prefixed(truncated + " A"),
	}
	gapB := EvidenceGap{
		Requirement: "verification:go test ./...",
		Status:      "failed",
		Detail:      truncated,
		DetailHash:  sha256Prefixed(truncated + " B"),
	}

	fingerprintA := GapFingerprint([]EvidenceGap{gapA})
	fingerprintB := GapFingerprint([]EvidenceGap{gapB})
	if fingerprintA == fingerprintB {
		t.Fatalf("fingerprints matched despite different full-detail hashes: %q", fingerprintA)
	}
}

func TestGapFingerprintIsOrderIndependent(t *testing.T) {
	gaps := []EvidenceGap{
		{Requirement: "verification:go test ./...", Status: "failed", Detail: "b"},
		{Requirement: "source_sanity", Status: "failed", Detail: "a"},
	}
	reversed := []EvidenceGap{gaps[1], gaps[0]}

	if GapFingerprint(gaps) != GapFingerprint(reversed) {
		t.Fatalf("fingerprint should be order independent:\n%s\n%s", GapFingerprint(gaps), GapFingerprint(reversed))
	}
}

func TestChildIdentityMismatchClassification(t *testing.T) {
	saved := &ChildRunIdentity{
		WorkspaceRootSHA256:        "root",
		ScopeContractSHA256:        "scope-a",
		VerificationCommandsSHA256: "commands",
		ToolManifestSHA256:         "tools",
		BackendProfile:             "profile",
		PlannerModel:               "planner",
		ExecutorModel:              "executor",
	}
	current := *saved
	current.ScopeContractSHA256 = "scope-b"

	record := CycleRecord{
		Status:        "dispatched",
		ChildIdentity: saved,
	}
	outcome := ClassifyCycleOutcome(record, &current)
	if !outcome.Blocking {
		t.Fatal("identity mismatch was not classified as blocking")
	}
	if outcome.Reason != "blocked_child_identity_mismatch:scope_contract_sha256" {
		t.Fatalf("Reason = %q", outcome.Reason)
	}
}
