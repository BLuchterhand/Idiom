package productevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/workflows/recovery"
)

func TestAddMalformedRecoveryArtifact(t *testing.T) {
	s := &productEvidenceRecovery{
		FailureClasses: map[string]int{},
		NextActions:    map[string]int{},
	}

	s.addMalformedRecoveryArtifact("decode_error", ".idiom/recovery/abc/failure.json")

	if s.MalformedArtifactCount != 1 {
		t.Fatalf("MalformedArtifactCount: got %d, want 1", s.MalformedArtifactCount)
	}
	if s.BlockedPacketCount != 1 {
		t.Fatalf("BlockedPacketCount: got %d, want 1", s.BlockedPacketCount)
	}
	if len(s.Blockers) != 1 {
		t.Fatalf("expected 1 blocker, got %#v", s.Blockers)
	}
	got := s.Blockers[0]
	if !strings.Contains(got, "decode_error") {
		t.Fatalf("blocker missing code: %q", got)
	}
	if !strings.Contains(got, "path_id:") {
		t.Fatalf("blocker missing path evidence (expected path_id: prefix): %q", got)
	}
}

func TestAddMalformedRecoveryArtifactMultipleAccumulate(t *testing.T) {
	s := &productEvidenceRecovery{
		FailureClasses: map[string]int{},
		NextActions:    map[string]int{},
	}
	s.addMalformedRecoveryArtifact("err_a", "path/a.json")
	s.addMalformedRecoveryArtifact("err_b", "path/b.json")

	if s.MalformedArtifactCount != 2 {
		t.Fatalf("MalformedArtifactCount: got %d, want 2", s.MalformedArtifactCount)
	}
	if s.BlockedPacketCount != 2 {
		t.Fatalf("BlockedPacketCount: got %d, want 2", s.BlockedPacketCount)
	}
	if len(s.Blockers) != 2 {
		t.Fatalf("expected 2 blockers, got %d", len(s.Blockers))
	}
}

func TestAddNextAction(t *testing.T) {
	t.Run("known action", func(t *testing.T) {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.addNextAction("review_decomposition")
		if s.NextActions["review_decomposition"] != 1 {
			t.Fatalf("expected review_decomposition count 1, got %#v", s.NextActions)
		}
	})

	t.Run("empty action becomes unknown", func(t *testing.T) {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.addNextAction("")
		if s.NextActions["unknown"] != 1 {
			t.Fatalf("empty action should map to unknown, got %#v", s.NextActions)
		}
	})

	t.Run("accumulates counts", func(t *testing.T) {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.addNextAction("review_decomposition")
		s.addNextAction("review_decomposition")
		s.addNextAction("answer_blocker")
		if s.NextActions["review_decomposition"] != 2 {
			t.Fatalf("expected review_decomposition count 2, got %#v", s.NextActions)
		}
		if s.NextActions["answer_blocker"] != 1 {
			t.Fatalf("expected answer_blocker count 1, got %#v", s.NextActions)
		}
	})
}

func TestFinishRecoveryEvidenceStatus(t *testing.T) {
	t.Run("not_available when no failure records", func(t *testing.T) {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.finishRecoveryEvidenceStatus()
		if s.Status != "not_available" {
			t.Fatalf("Status: got %q, want not_available", s.Status)
		}
		if s.ReleaseImpact != "no local recovery artifacts found" {
			t.Fatalf("ReleaseImpact: got %q", s.ReleaseImpact)
		}
		if s.FailureClasses != nil {
			t.Fatalf("empty FailureClasses should be nil after finish, got %#v", s.FailureClasses)
		}
		if s.NextActions != nil {
			t.Fatalf("empty NextActions should be nil after finish, got %#v", s.NextActions)
		}
	})

	t.Run("blocked when malformed artifacts present", func(t *testing.T) {
		s := &productEvidenceRecovery{
			MalformedArtifactCount: 1,
			BlockedPacketCount:     1,
			FailureClasses:         map[string]int{},
			NextActions:            map[string]int{},
			Blockers:               []string{"err:path"},
		}
		s.finishRecoveryEvidenceStatus()
		if s.Status != "blocked" {
			t.Fatalf("Status: got %q, want blocked", s.Status)
		}
		if !strings.Contains(s.ReleaseImpact, "malformed") {
			t.Fatalf("ReleaseImpact should mention malformed: %q", s.ReleaseImpact)
		}
	})

	t.Run("blocked when blocked packets present", func(t *testing.T) {
		s := &productEvidenceRecovery{
			BlockedPacketCount: 2,
			FailureClasses:     map[string]int{},
			NextActions:        map[string]int{},
			Blockers:           []string{"missing"},
		}
		s.finishRecoveryEvidenceStatus()
		if s.Status != "blocked" {
			t.Fatalf("Status: got %q, want blocked", s.Status)
		}
	})

	t.Run("partial when open packets present", func(t *testing.T) {
		s := &productEvidenceRecovery{
			FailureRecordCount:  1,
			OpenPacketCount:     1,
			RemainingAtomCount:  3,
			FailureClasses:      map[string]int{"executor_edit_failed": 1},
			NextActions:         map[string]int{"review_decomposition": 1},
		}
		s.finishRecoveryEvidenceStatus()
		if s.Status != "partial" {
			t.Fatalf("Status: got %q, want partial", s.Status)
		}
		if !strings.Contains(s.ReleaseImpact, "open recovery packets") {
			t.Fatalf("ReleaseImpact should mention open packets: %q", s.ReleaseImpact)
		}
	})

	t.Run("partial when partial reconciliation packets", func(t *testing.T) {
		s := &productEvidenceRecovery{
			FailureRecordCount:  1,
			PartialPacketCount:  1,
			RemainingAtomCount:  2,
			FailureClasses:      map[string]int{"executor_edit_failed": 1},
			NextActions:         map[string]int{"review_decomposition": 1},
		}
		s.finishRecoveryEvidenceStatus()
		if s.Status != "partial" {
			t.Fatalf("Status: got %q, want partial", s.Status)
		}
	})

	t.Run("pass when all reconciled", func(t *testing.T) {
		s := &productEvidenceRecovery{
			FailureRecordCount:    1,
			DecompositionPacketCount: 1,
			ReconciliationCount:   1,
			RecoveredPacketCount:  1,
			CompletedAtomCount:    2,
			FailureClasses:        map[string]int{"executor_edit_failed": 1},
			NextActions:           map[string]int{"none": 1},
		}
		s.finishRecoveryEvidenceStatus()
		if s.Status != "pass" {
			t.Fatalf("Status: got %q, want pass", s.Status)
		}
		if s.ReleaseImpact != "all local recovery packets with evidence are reconciled" {
			t.Fatalf("ReleaseImpact: got %q", s.ReleaseImpact)
		}
	})
}

func TestRecoveryEvidenceArtifactHashPart(t *testing.T) {
	root := t.TempDir()

	t.Run("empty relPath returns empty", func(t *testing.T) {
		got := recoveryEvidenceArtifactHashPart(root, "")
		if got != "" {
			t.Fatalf("expected empty for empty path, got %q", got)
		}
	})

	t.Run("whitespace relPath returns empty", func(t *testing.T) {
		got := recoveryEvidenceArtifactHashPart(root, "   ")
		if got != "" {
			t.Fatalf("expected empty for whitespace path, got %q", got)
		}
	})

	t.Run("missing file returns path:missing", func(t *testing.T) {
		got := recoveryEvidenceArtifactHashPart(root, ".idiom/recovery/nonexistent.json")
		if !strings.Contains(got, ":missing") {
			t.Fatalf("expected :missing for nonexistent file, got %q", got)
		}
	})

	t.Run("present file returns path:hash", func(t *testing.T) {
		relPath := ".idiom/recovery/test-failure/failure.json"
		fullPath := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte("test content"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		got := recoveryEvidenceArtifactHashPart(root, relPath)
		if !strings.HasPrefix(got, relPath+":") {
			t.Fatalf("expected prefix %q, got %q", relPath+":", got)
		}
		if strings.Contains(got, ":missing") {
			t.Fatalf("should not be missing: %q", got)
		}
		// Same content should produce same hash
		got2 := recoveryEvidenceArtifactHashPart(root, relPath)
		if got2 != got {
			t.Fatalf("same path should produce same hash: %q vs %q", got, got2)
		}
	})
}

func TestRecoveryEvidenceArtifactID(t *testing.T) {
	tests := []struct {
		kind    string
		relPath string
	}{
		{"failure_record", ".idiom/recovery/abc/failure.json"},
		{"decomposition_packet", ".idiom/recovery/abc/decomposition_packet.json"},
		{"reconciliation", ".idiom/recovery/abc/reconciliation.json"},
	}
	for _, tt := range tests {
		got := recoveryEvidenceArtifactID(tt.kind, tt.relPath)
		if !strings.HasPrefix(got, tt.kind+":") {
			t.Fatalf("expected ID to start with %q, got %q", tt.kind+":", got)
		}
		if len(got) <= len(tt.kind)+1 {
			t.Fatalf("expected stable ID component after prefix, got %q", got)
		}
	}
}

func TestRecoveryEvidencePacketBlockers(t *testing.T) {
	t.Run("packet with non-review next action blocks", func(t *testing.T) {
		packet := recovery.DecompositionPacket{
			NextAction: recovery.NextActionInspectFailure,
			PartialState: recovery.PartialState{
				DirtyStatus: recovery.DirtyStatusClean,
				ScopeStatus: recovery.ScopeStatusInScope,
			},
			CandidateAtoms: []recovery.CandidateAtom{{ID: "atom-1"}},
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		if len(blockers) == 0 {
			t.Fatal("expected blockers for non-review next action")
		}
		if !strings.Contains(blockers[0], "inspect_failure") {
			t.Fatalf("expected blocker to mention inspect_failure, got %#v", blockers)
		}
	})

	t.Run("empty candidate atoms blocks", func(t *testing.T) {
		packet := recovery.DecompositionPacket{
			NextAction:     recovery.NextActionReviewDecomposition,
			CandidateAtoms: nil,
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		if len(blockers) == 0 {
			t.Fatal("expected blockers for empty candidate atoms")
		}
		if !stringSliceContains(blockers, "packet has no candidate recovery atoms") {
			t.Fatalf("expected 'no candidate recovery atoms' blocker, got %#v", blockers)
		}
	})

	t.Run("dirty out of scope blocks", func(t *testing.T) {
		packet := recovery.DecompositionPacket{
			NextAction: recovery.NextActionReviewDecomposition,
			PartialState: recovery.PartialState{
				DirtyStatus: recovery.DirtyStatusDirtyOutScope,
				ScopeStatus: recovery.ScopeStatusInScope,
			},
			CandidateAtoms: []recovery.CandidateAtom{{ID: "atom-1"}},
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		if !stringSliceContains(blockers, "partial state includes out-of-scope dirty paths") {
			t.Fatalf("expected dirty out-of-scope blocker, got %#v", blockers)
		}
	})

	t.Run("scope out of scope blocks", func(t *testing.T) {
		packet := recovery.DecompositionPacket{
			NextAction: recovery.NextActionReviewDecomposition,
			PartialState: recovery.PartialState{
				DirtyStatus: recovery.DirtyStatusClean,
				ScopeStatus: recovery.ScopeStatusOutOfScope,
			},
			CandidateAtoms: []recovery.CandidateAtom{{ID: "atom-1"}},
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		if !stringSliceContains(blockers, "partial state is out of recovery scope") {
			t.Fatalf("expected out-of-scope blocker, got %#v", blockers)
		}
	})

	t.Run("clean in-scope packet with review action has no blockers", func(t *testing.T) {
		packet := recovery.DecompositionPacket{
			NextAction: recovery.NextActionReviewDecomposition,
			PartialState: recovery.PartialState{
				DirtyStatus: recovery.DirtyStatusClean,
				ScopeStatus: recovery.ScopeStatusInScope,
			},
			CandidateAtoms: []recovery.CandidateAtom{{ID: "atom-1"}},
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		if len(blockers) != 0 {
			t.Fatalf("expected no blockers for clean packet, got %#v", blockers)
		}
	})
}
