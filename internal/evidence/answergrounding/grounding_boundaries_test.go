package answergrounding

import "testing"

func TestClaimExtractor_ExtractsClaimsWithoutReceipts(t *testing.T) {
	answer := "Verification passed.\nChanged the implementation.\nThe definition is at internal/labels/labels.go:3-5."

	claims := (ClaimExtractor{}).Extract(answer)

	if len(claims) != 3 {
		t.Fatalf("claims length = %d, want 3: %#v", len(claims), claims)
	}

	commandClaim := mustFindClaim(t, claims, ClaimCommandResult)
	if commandClaim.Supported || len(commandClaim.EvidenceIDs) != 0 {
		t.Fatalf("command claim should be extracted without support: %#v", commandClaim)
	}

	if commandClaim.Severity != SeverityBlocking {
		t.Fatalf("command claim severity = %q, want blocking", commandClaim.Severity)
	}

	changeClaim := mustFindClaim(t, claims, Change)
	if changeClaim.Supported || changeClaim.Target != TargetChangedPath {
		t.Fatalf("change claim = %#v, want unsupported changed_path claim", changeClaim)
	}

	sourceClaim := mustFindClaim(t, claims, SourceLocation)
	if sourceClaim.Supported {
		t.Fatalf("source claim should be extracted before support lookup: %#v", sourceClaim)
	}

	if sourceClaim.Target != "internal/labels/labels.go" {
		t.Fatalf("source claim target = %q, want normalized target", sourceClaim.Target)
	}

	if sourceClaim.Line != 3 {
		t.Fatalf("source claim line = %d, want cited start line 3", sourceClaim.Line)
	}

	if sourceClaim.Text != "internal/labels/labels.go:3-5" {
		t.Fatalf("source claim text = %q, want cited span", sourceClaim.Text)
	}
}

func TestEvidenceLedger_LooksUpSupportWithoutClaimExtraction(t *testing.T) {
	ledger := NewEvidenceLedger([]EvidenceReceipt{
		{
			ID:       "p1",
			Kind:     PathSeen,
			Target:   "`internal\\labels/labels.go,`",
			Supports: []ClaimKind{Unsupported},
			Fresh:    false,
		},
		{
			ID:        "s1",
			Kind:      SourceBody,
			Target:    "internal/labels/labels.go",
			StartLine: 10,
			EndLine:   20,
			Fresh:     false,
		},
		{
			ID:    "c1",
			Kind:  CommandResult,
			Fresh: false,
		},
	})

	receipt, ok := ledger.FindTarget("internal/labels/labels.go", PathSeen)
	if !ok || receipt.ID != "p1" {
		t.Fatalf("FindTarget PathSeen = (%#v, %v), want p1", receipt, ok)
	}

	receipt, ok = ledger.FindCoveredTarget("internal/labels/labels.go", 12, 18, SourceBody)
	if !ok || receipt.ID != "s1" {
		t.Fatalf("FindCoveredTarget SourceBody = (%#v, %v), want s1", receipt, ok)
	}

	if receipt, ok = ledger.FindCoveredTarget("internal/labels/labels.go", 9, 18, SourceBody); ok {
		t.Fatalf("FindCoveredTarget outside range = (%#v, true), want false", receipt)
	}

	receipt, ok = ledger.FindAny(CommandResult)
	if !ok || receipt.ID != "c1" {
		t.Fatalf("FindAny CommandResult = (%#v, %v), want c1", receipt, ok)
	}

	if receipt, ok = ledger.FindTarget("internal/labels/labels.go", Packet); ok {
		t.Fatalf("FindTarget with disallowed kind = (%#v, true), want false", receipt)
	}
}

func TestGroundingValidator_AppliesSupportTierRules(t *testing.T) {
	sourceClaim := AnswerClaim{
		Text:     "internal/labels/labels.go:3-5",
		Kind:     SourceLocation,
		Target:   "internal/labels/labels.go",
		Line:     3,
		Severity: SeverityBlocking,
		Reason:   "source location claim requires source_body or symbol_body evidence covering the cited line",
	}
	pathClaim := AnswerClaim{
		Text:     "internal/labels/labels.go",
		Kind:     Path,
		Target:   "internal/labels/labels.go",
		Line:     1,
		Severity: SeverityBlocking,
		Reason:   "path reference requires path or packet evidence",
	}
	commandClaim := AnswerClaim{
		Text:     "Verification passed.",
		Kind:     ClaimCommandResult,
		Target:   "verification",
		Line:     1,
		Severity: SeverityBlocking,
		Reason:   "verification claim requires command_result evidence",
	}
	changeClaim := AnswerClaim{
		Text:     "Changed the implementation.",
		Kind:     Change,
		Target:   "changed_path",
		Line:     1,
		Severity: SeverityBlocking,
		Reason:   "change claim requires diff_write evidence",
	}

	packetOnly := NewGroundingValidator([]EvidenceReceipt{
		{ID: "p1", Kind: Packet, Target: "internal/labels/labels.go"},
	})
	packetValidated := packetOnly.Validate([]AnswerClaim{sourceClaim, pathClaim})
	assertUnsupported(t, packetValidated[0], SourceLocation)
	assertSupportedBy(t, packetValidated[1], Path, "p1")

	shortSource := NewGroundingValidator([]EvidenceReceipt{
		{ID: "s1", Kind: SourceBody, Target: "internal/labels/labels.go", StartLine: 1, EndLine: 4},
	})
	rangeValidated := shortSource.Validate([]AnswerClaim{sourceClaim})
	assertUnsupported(t, rangeValidated[0], SourceLocation)

	validator := NewGroundingValidator([]EvidenceReceipt{
		{ID: "p1", Kind: Packet, Target: "internal/labels/labels.go"},
		{ID: "s2", Kind: SourceBody, Target: "internal/labels/labels.go", StartLine: 1, EndLine: 5},
		{ID: "c1", Kind: CommandResult, Fresh: false},
		{ID: "d1", Kind: DiffWrite, Fresh: false},
	})
	validated := validator.Validate([]AnswerClaim{sourceClaim, pathClaim, commandClaim, changeClaim})

	assertSupportedBy(t, validated[0], SourceLocation, "s2")
	assertSupportedBy(t, validated[1], Path, "p1")
	assertSupportedBy(t, validated[2], ClaimCommandResult, "c1")
	assertSupportedBy(t, validated[3], Change, "d1")
}

func TestGroundingValidator_SourceClaimRangeFallbacks(t *testing.T) {
	validator := NewGroundingValidator([]EvidenceReceipt{
		{ID: "s1", Kind: SymbolBody, Target: "internal/labels/labels.go", StartLine: 8, EndLine: 8},
	})

	notSource := AnswerClaim{
		Text:     "internal/other/labels.go",
		Kind:     Path,
		Target:   "internal/other/labels.go",
		Line:     8,
		Severity: SeverityBlocking,
	}
	sourceWithoutCitation := AnswerClaim{
		Text:     "definition location",
		Kind:     SourceLocation,
		Target:   "internal/labels/labels.go",
		Line:     8,
		Severity: SeverityBlocking,
	}

	if got := validator.ValidateClaim(notSource); got.Supported {
		t.Fatalf("path claim should not be supported by a different target: %#v", got)
	}

	assertSupportedBy(t, validator.ValidateClaim(sourceWithoutCitation), SourceLocation, "s1")
}

func TestTargetNormalizer_PreservesLegacyCompatibility(t *testing.T) {
	normalizer := TargetNormalizer{}
	cases := []struct {
		name   string
		target string
		want   string
	}{
		{
			name:   "trim punctuation and backslash",
			target: " `internal\\labels/labels.go,` ",
			want:   "internal/labels/labels.go",
		},
		{
			name:   "preserve legacy leading slash after relative prefix",
			target: "./internal/labels/labels.go",
			want:   "/internal/labels/labels.go",
		},
		{
			name:   "empty target",
			target: " ,.; ",
			want:   "",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizer.Normalize(tt.target); got != tt.want {
				t.Fatalf("TargetNormalizer.Normalize(%q) = %q, want %q", tt.target, got, tt.want)
			}

			if got := normalizeTarget(tt.target); got != tt.want {
				t.Fatalf("normalizeTarget(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}

	if !normalizer.Same("`internal\\labels/labels.go,`", "internal/labels/labels.go") {
		t.Fatal("TargetNormalizer.Same should match legacy-normalized targets")
	}
}

func mustFindClaim(t *testing.T, claims []AnswerClaim, kind ClaimKind) AnswerClaim {
	t.Helper()

	for _, claim := range claims {
		if claim.Kind == kind {
			return claim
		}
	}

	t.Fatalf("missing %s claim in %#v", kind, claims)

	return AnswerClaim{}
}

func assertSupportedBy(t *testing.T, claim AnswerClaim, kind ClaimKind, evidenceID string) {
	t.Helper()

	if claim.Kind != kind {
		t.Fatalf("claim kind = %s, want %s: %#v", claim.Kind, kind, claim)
	}

	if !claim.Supported {
		t.Fatalf("claim should be supported: %#v", claim)
	}

	if claim.Severity != "" || claim.Reason != "" {
		t.Fatalf("supported claim should clear severity and reason: %#v", claim)
	}

	if len(claim.EvidenceIDs) != 1 || claim.EvidenceIDs[0] != evidenceID {
		t.Fatalf("claim evidence = %#v, want [%s]", claim.EvidenceIDs, evidenceID)
	}
}

func assertUnsupported(t *testing.T, claim AnswerClaim, kind ClaimKind) {
	t.Helper()

	if claim.Kind != kind {
		t.Fatalf("claim kind = %s, want %s: %#v", claim.Kind, kind, claim)
	}

	if claim.Supported {
		t.Fatalf("claim should be unsupported: %#v", claim)
	}

	if claim.Severity != SeverityBlocking {
		t.Fatalf("claim severity = %q, want blocking: %#v", claim.Severity, claim)
	}
}
