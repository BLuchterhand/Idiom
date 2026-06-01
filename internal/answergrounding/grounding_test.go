package answergrounding

import (
	"testing"
)

func TestEvaluate_AcceptsPathOnlyEvidence(t *testing.T) {
	// Test: accepts path-only evidence after path_seen/context packet.
	receipts := []EvidenceReceipt{
		{
			ID:       "r1",
			Kind:     PathSeen,
			Target:   "internal/labels/labels.go",
			Supports: []ClaimKind{Path},
		},
	}
	answer := "The file internal/labels/labels.go exists."
	result := Evaluate(answer, receipts)

	if len(result.Claims) == 0 {
		t.Fatal("Expected at least one claim")
	}

	// Find the path claim
	var pathClaim *AnswerClaim
	for i, c := range result.Claims {
		if c.Kind == Path {
			pathClaim = &result.Claims[i]
			break
		}
	}

	if pathClaim == nil {
		t.Fatal("Expected a path claim")
	}

	if !pathClaim.Supported {
		t.Error("Expected path claim to be supported by path_seen evidence")
	}
}

func TestEvaluate_AcceptsSourceBodyEvidence(t *testing.T) {
	// Test: accepts internal/labels/labels.go:3 with source_body lines 1-5.
	receipts := []EvidenceReceipt{
		{
			ID:        "r1",
			Kind:      SourceBody,
			Target:    "internal/labels/labels.go",
			StartLine: 1,
			EndLine:   5,
			Supports:  []ClaimKind{SourceLocation},
		},
	}
	answer := "The definition is at internal/labels/labels.go:3."
	result := Evaluate(answer, receipts)

	if len(result.Claims) == 0 {
		t.Fatal("Expected at least one claim")
	}

	var sourceClaim *AnswerClaim
	for i, c := range result.Claims {
		if c.Kind == SourceLocation {
			sourceClaim = &result.Claims[i]
			break
		}
	}

	if sourceClaim == nil {
		t.Fatal("Expected a source_location claim")
	}

	if !sourceClaim.Supported {
		t.Error("Expected source_location claim to be supported by source_body evidence")
	}
}

func TestEvaluate_RejectsSourceLocationWithOnlyPathSeen(t *testing.T) {
	// Test: rejects the same line claim with only path_seen.
	receipts := []EvidenceReceipt{
		{
			ID:       "r1",
			Kind:     PathSeen,
			Target:   "internal/labels/labels.go",
			Supports: []ClaimKind{Path},
		},
	}
	answer := "The definition is at internal/labels/labels.go:3."
	result := Evaluate(answer, receipts)

	if len(result.Claims) == 0 {
		t.Fatal("Expected at least one claim")
	}

	var sourceClaim *AnswerClaim
	for i, c := range result.Claims {
		if c.Kind == SourceLocation {
			sourceClaim = &result.Claims[i]
			break
		}
	}

	if sourceClaim == nil {
		t.Fatal("Expected a source_location claim")
	}

	if sourceClaim.Supported {
		t.Error("Expected source_location claim to be unsupported by only path_seen evidence")
	}

	if sourceClaim.Severity != "blocking" {
		t.Error("Expected source_location claim to be blocking")
	}
}

func TestEvaluate_RejectsNonexistentSourceCitation(t *testing.T) {
	// Test: rejects nonexistent/current source citation.
	receipts := []EvidenceReceipt{
		{
			ID:        "r1",
			Kind:      SourceBody,
			Target:    "internal/labels/labels.go",
			StartLine: 1,
			EndLine:   5,
			Supports:  []ClaimKind{SourceLocation},
		},
	}
	// Claiming a line outside the evidence range
	answer := "The definition is at internal/labels/labels.go:10."
	result := Evaluate(answer, receipts)

	if len(result.Claims) == 0 {
		t.Fatal("Expected at least one claim")
	}

	var sourceClaim *AnswerClaim
	for i, c := range result.Claims {
		if c.Kind == SourceLocation {
			sourceClaim = &result.Claims[i]
			break
		}
	}

	if sourceClaim == nil {
		t.Fatal("Expected a source_location claim")
	}

	if sourceClaim.Supported {
		t.Error("Expected source_location claim to be unsupported for line 10 when evidence is 1-5")
	}
}

func TestEvaluate_AcceptsBacktickedLiterals(t *testing.T) {
	// Test: accepts backticked `go test ./...`, `read_file`, `start_line`, and `Save` as literals without evidence.
	receipts := []EvidenceReceipt{}
	answer := "Run `go test ./...` and use `read_file` with `start_line` and `Save`."
	result := Evaluate(answer, receipts)

	// The implementation should not generate blocking claims for literals.
	// It might generate no claims or supported claims.
	for _, c := range result.Claims {
		if c.Severity == "blocking" {
			t.Errorf("Expected no blocking claims for literals, got: %s", c.Text)
		}
	}
}

func TestEvaluate_RejectsVerificationPassedWithoutCommandResult(t *testing.T) {
	// Test: rejects “verification passed” with no command_result receipt.
	receipts := []EvidenceReceipt{}
	answer := "Verification passed."
	result := Evaluate(answer, receipts)

	if len(result.Claims) == 0 {
		t.Fatal("Expected at least one claim")
	}

	var cmdClaim *AnswerClaim
	for i, c := range result.Claims {
		if c.Kind == ClaimCommandResult {
			cmdClaim = &result.Claims[i]
			break
		}
	}

	if cmdClaim == nil {
		t.Fatal("Expected a command_result claim")
	}

	if cmdClaim.Supported {
		t.Error("Expected command_result claim to be unsupported without command_result receipt")
	}

	if cmdClaim.Severity != "blocking" {
		t.Error("Expected command_result claim to be blocking")
	}
}

func TestEvaluate_RejectsChangeWithoutDiffWrite(t *testing.T) {
	// Test: rejects “changed X” with no diff_write/change receipt.
	receipts := []EvidenceReceipt{}
	answer := "Changed the implementation."
	result := Evaluate(answer, receipts)

	if len(result.Claims) == 0 {
		t.Fatal("Expected at least one claim")
	}

	var changeClaim *AnswerClaim
	for i, c := range result.Claims {
		if c.Kind == Change {
			changeClaim = &result.Claims[i]
			break
		}
	}

	if changeClaim == nil {
		t.Fatal("Expected a change claim")
	}

	if changeClaim.Supported {
		t.Error("Expected change claim to be unsupported without diff_write receipt")
	}

	if changeClaim.Severity != "blocking" {
		t.Error("Expected change claim to be blocking")
	}
}
