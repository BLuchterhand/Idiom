package intentloop

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSaveLoadCyclePromptShapeAndHash(t *testing.T) {
	root := t.TempDir()
	originalPrompt := `Implement the feature.

Typed plan execution contract:
{"atoms":[{"atom_id":"impl","required_tests":[{"command":"go test ./..."}]}]}`
	state := NewState("session-1", originalPrompt, IntentLoopContract{}, 5, testNow())
	state.Cycles = []CycleRecord{{
		CycleNumber:        1,
		Status:             "completed",
		ChangedPaths:       []string{"internal/foo/foo.go"},
		SourceSanityPassed: true,
		RemainingFailures: []EvidenceGap{{
			Requirement: "verification:go vet ./...",
			Status:      "failed",
			Detail:      strings.Repeat("x", maxPromptGapDetailRunes+20),
			DetailHash:  sha256Prefixed(strings.Repeat("x", maxPromptGapDetailRunes+20)),
		}},
	}}

	prompt := BuildCyclePrompt(originalPrompt, state)
	for _, want := range []string{
		"## Original Intent",
		"Typed plan execution contract:",
		"## Prior Cycles",
		"## Remaining Requirements",
		"## Instructions",
		"detail_hash: `sha256:",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, strings.Repeat("x", maxPromptGapDetailRunes+1)) {
		t.Fatal("remaining requirement detail was not bounded")
	}

	relPath, hash, err := SaveCyclePrompt(root, state.SessionID, 2, prompt)
	if err != nil {
		t.Fatalf("SaveCyclePrompt() error = %v", err)
	}
	if hash != sha256Hex(prompt) {
		t.Fatalf("hash = %q, want %q", hash, sha256Hex(prompt))
	}
	loaded, err := LoadCyclePrompt(root, relPath, hash)
	if err != nil {
		t.Fatalf("LoadCyclePrompt() error = %v", err)
	}
	if loaded != prompt {
		t.Fatal("loaded prompt differs from saved prompt")
	}
}

func testNow() time.Time {
	return time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
}
