package comprehension

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("new compact pack projection from pack result", func() {
	result := PackResult{
		PackID:       "test-pack-123",
		Fresh:        true,
		BudgetTokens: 500,
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"internal/foo/bar.go", "internal/foo/baz.go"},
			Reasons:                []string{"source_owner_token_overlap_high"},
		},
		CandidateFiles: []PackFile{
			{Path: "internal/foo/bar.go", Role: "source", Reason: "top match"},
			{Path: "internal/foo/baz.go", Role: "source", Reason: "related"},
		},
		CandidateSymbols: []PackSymbol{
			{ID: "sym1", QualifiedName: "DoFoo", Path: "internal/foo/bar.go", Kind: "func", StartLine: 10, Reason: "relevant"},
		},
		Tests: []PackTest{
			{Command: "go test ./internal/foo/...", Path: "internal/foo/bar_test.go", Symbol: "TestDoFoo"},
		},
		UnresolvedGaps: []string{"gap1"},
	}

	p := NewCompactPackProjection(result)
	Expect(

		// PackID
		p.PackID).To(BeEquivalentTo("test-pack-123"), "expected PackID 'test-pack-123', got %q", p.PackID)
	Expect(

		// Fresh
		p.Fresh).To(BeTrue(), "expected Fresh=true")
	Expect(

		// Quality
		p.Quality.LocalizationConfidence).To(BeEquivalentTo("high"), "expected LocalizationConfidence 'high', got %q", p.Quality.LocalizationConfidence)
	Expect(p.Quality.SourceOwnerFound).To(BeTrue(), "expected SourceOwnerFound=true")
	Expect(len(p.Quality.EvidencePaths)).To(BeEquivalentTo(2), "expected 2 EvidencePaths, got %d", len(p.Quality.EvidencePaths))
	Expect(len(p.Quality.Reasons)).To(BeEquivalentTo(1), "expected 1 Reason, got %d", len(p.Quality.Reasons))
	Expect(

		// CandidateFiles
		len(p.CandidateFiles)).To(BeEquivalentTo(2), "expected 2 CandidateFiles, got %d", len(p.CandidateFiles))
	Expect(p.CandidateFiles[0].Path).To(BeEquivalentTo("internal/foo/bar.go"), "expected file path 'internal/foo/bar.go', got %q", p.CandidateFiles[0].Path)
	Expect(p.CandidateFiles[0].Role).To(BeEquivalentTo("source"), "expected role 'source', got %q", p.CandidateFiles[0].Role)
	Expect(

		// CandidateSymbols
		len(p.CandidateSymbols)).To(BeEquivalentTo(1), "expected 1 CandidateSymbol, got %d", len(p.CandidateSymbols))
	Expect(p.CandidateSymbols[0].QualifiedName).To(BeEquivalentTo("DoFoo"), "expected QualifiedName 'DoFoo', got %q", p.CandidateSymbols[0].QualifiedName)
	Expect(

		// Tests
		len(p.Tests)).To(BeEquivalentTo(1), "expected 1 Test, got %d", len(p.Tests))
	Expect(

		// UnresolvedGaps
		len(p.UnresolvedGaps)).To(BeEquivalentTo(1), "expected 1 UnresolvedGap, got %d", len(p.UnresolvedGaps))
	Expect(

		// BudgetTokens
		p.BudgetTokens).To(BeEquivalentTo(500), "expected BudgetTokens 500, got %d", p.BudgetTokens)
	Expect(

		// TotalCandidates
		p.TotalCandidates).To(BeEquivalentTo(4), // 2 files + 1 symbol + 1 test
		"expected TotalCandidates 4, got %d", p.TotalCandidates)
})
var _ = It("attach pack result compact projection includes late quality gaps", func() {
	result := PackResult{
		PackID:       "weak-source-owner",
		Fresh:        true,
		BudgetTokens: 500,
		Quality: PackQuality{
			LocalizationConfidence: "low",
			SourceOwnerFound:       false,
		},
		CandidateFiles: []PackFile{
			{Path: "internal/related.go", Role: "related", Reason: "term overlap"},
		},
	}

	addPackResultGaps(&packContext{status: StatusReport{Fresh: true}}, packCandidates{}, &result)
	attachPackResultCompactProjection(&result)
	Expect(slices.Contains(result.CompactProjection.UnresolvedGaps, "reason=weak_source_owner localization_confidence=low")).To(BeTrue(), "CompactProjection.UnresolvedGaps = %#v, want weak_source_owner", result.CompactProjection.UnresolvedGaps)

	rendered, _ := RenderCompactPackProjection(result.CompactProjection, result.BudgetTokens)
	Expect(strings.Contains(rendered, "weak_source_owner")).To(BeTrue(), "rendered compact projection missing weak_source_owner:\n%s", rendered)
})
var _ = It("render compact pack projection no budget", func() {
	result := PackResult{
		PackID: "pack-no-budget",
		Fresh:  true,
		Quality: PackQuality{
			LocalizationConfidence: "medium",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"pkg/handler.go"},
			Reasons:                []string{"source_owner_token_overlap"},
		},
		CandidateFiles: []PackFile{
			{Path: "pkg/handler.go", Role: "source", Reason: "handler"},
			{Path: "pkg/service.go", Role: "source", Reason: "service logic"},
		},
		CandidateSymbols: []PackSymbol{
			{QualifiedName: "HandleRequest", Path: "pkg/handler.go", Kind: "method", StartLine: 42, Reason: "request handler"},
		},
		Tests: []PackTest{
			{Command: "go test ./pkg/...", Path: "pkg/handler_test.go", Symbol: "TestHandleRequest"},
		},
		UnresolvedGaps: []string{"stale index"},
	}

	p := NewCompactPackProjection(result)
	out, returned := RenderCompactPackProjection(p, 0)
	Expect( // no budget

		// Quality must be present
		strings.Contains(out, "packet_quality:")).To(BeTrue(), "expected packet_quality section in output")
	Expect(strings.Contains(out, "localization_confidence: medium")).To(BeTrue(), "expected localization_confidence in output")
	Expect(strings.Contains(out, "source_owner_found: true")).To(BeTrue(), "expected source_owner_found in output")
	Expect(

		// pack_id must be present
		strings.Contains(out, "pack_id: pack-no-budget")).To(BeTrue(), "expected pack_id in output")
	Expect(

		// fresh must be rendered
		strings.Contains(out, "fresh: true")).To(BeTrue(), "expected fresh: true in output")
	Expect(

		// evidence_note must be present
		strings.Contains(out, "evidence_note: context_pack is localization evidence")).To(BeTrue(), "expected evidence_note in output")
	Expect(

		// All items rendered (no budget enforcement)
		strings.Contains(out, "pkg/handler.go")).To(BeTrue(), "expected handler.go in output")
	Expect(strings.Contains(out, "pkg/service.go")).To(BeTrue(), "expected service.go in output")
	Expect(strings.Contains(out, "HandleRequest")).To(BeTrue(), "expected HandleRequest in output")
	Expect(

		// No omitted section when no budget
		strings.Contains(out, "omitted:")).To(BeFalse(), "unexpected omitted section when BudgetTokens=0")
	Expect(

		// Returned projection should have zero omitted counts
		returned.OmittedFiles).To(BeEquivalentTo(0), "expected OmittedFiles=0 in unbounded mode, got %d", returned.OmittedFiles)
	Expect(returned.OmittedSymbols).To(BeEquivalentTo(0), "expected OmittedSymbols=0 in unbounded mode, got %d", returned.OmittedSymbols)
	Expect(returned.OmittedTests).To(BeEquivalentTo(0), "expected OmittedTests=0 in unbounded mode, got %d", returned.OmittedTests)
})
var _ = It("render compact pack projection budget truncation", func() {
	// Create a PackResult with many candidates
	files := make([]PackFile, 20)
	for i := range files {
		files[i] = PackFile{
			Path:   "pkg/file_" + strings.Repeat("x", 40) + ".go",
			Role:   "source",
			Reason: "relevant to task",
		}
	}

	symbols := make([]PackSymbol, 10)
	for i := range symbols {
		symbols[i] = PackSymbol{
			QualifiedName: "Func_" + strings.Repeat("x", 30),
			Path:          "pkg/file.go",
			Kind:          "func",
			StartLine:     i * 10,
			Reason:        "symbol match",
		}
	}

	tests := make([]PackTest, 5)
	for i := range tests {
		tests[i] = PackTest{
			Command: "go test ./pkg/...",
			Path:    "pkg/file_test.go",
			Symbol:  "TestFunc",
		}
	}

	result := PackResult{
		PackID: "pack-budget-test",
		Fresh:  true,
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"pkg/file.go"},
			Reasons:                []string{"source_owner_token_overlap_high"},
		},
		CandidateFiles:   files,
		CandidateSymbols: symbols,
		Tests:            tests,
		BudgetTokens:     300,
	}

	p := NewCompactPackProjection(result)
	out, returned := RenderCompactPackProjection(p, 300)

	// Output must be under budget (hard cap contract)
	est := estimateTokens(out)
	Expect(est > 300).To(BeFalse(), "estimateTokens(output) = %d, must be <= budgetTokens=%d", est, 300)

	// Omitted counts must be populated on the returned struct
	totalOmitted := returned.OmittedFiles + returned.OmittedSymbols + returned.OmittedTests
	Expect(totalOmitted).NotTo(BeEquivalentTo(0), "expected at least one omitted count > 0 with BudgetTokens=300 and 35 candidates")
	Expect(

		// pack_id and quality always present
		strings.Contains(out, "pack_id: pack-budget-test")).To(BeTrue(), "expected pack_id in truncated output")
	Expect(strings.Contains(out, "packet_quality:")).To(BeTrue(), "expected packet_quality in truncated output")
	Expect(strings.Contains(out, "localization_confidence: high")).To(BeTrue(), "expected localization_confidence in truncated output")
	Expect(strings.Contains(out, "source_owner_found: true")).To(BeTrue(), "expected source_owner_found in truncated output")
	Expect(

		// fresh must be rendered
		strings.Contains(out, "fresh: true")).To(BeTrue(), "expected fresh: true in output")
	Expect(

		// evidence_note always present
		strings.Contains(out, "evidence_note:")).To(BeTrue(), "expected evidence_note in truncated output")
	Expect(

		// Omitted section should be present
		strings.Contains(out, "omitted:")).To(BeTrue(), "expected omitted section with BudgetTokens=300 and 35 candidates")
})
var _ = It("render compact pack projection budget tokens zero no truncation", func() {
	files := make([]PackFile, 15)
	for i := range files {
		files[i] = PackFile{Path: "pkg/f.go", Role: "source", Reason: "match"}
	}

	result := PackResult{
		PackID: "pack-unbounded",
		Fresh:  false,
		Quality: PackQuality{
			LocalizationConfidence: "low",
		},
		CandidateFiles: files,
	}

	p := NewCompactPackProjection(result)
	out, returned := RenderCompactPackProjection(p, 0)
	Expect(

		// fresh: false must be rendered
		strings.Contains(out, "fresh: false")).To(BeTrue(), "expected fresh: false in output")
	Expect(

		// No omitted section
		strings.Contains(out, "omitted:")).To(BeFalse(), "unexpected omitted section when BudgetTokens=0")
	Expect(

		// Returned projection has zero omitted counts
		returned.OmittedFiles).To(BeEquivalentTo(0), "expected OmittedFiles=0, got %d", returned.OmittedFiles)

	// All 15 files rendered (no truncation)
	count := strings.Count(out, "pkg/f.go")
	Expect(count < 15).To(BeFalse(), "expected all 15 files rendered with BudgetTokens=0, got %d occurrences", count)
})
var _ = It("estimate tokens", func() {
	tests := []struct {
		text    string
		wantMin int
		wantMax int
	}{
		{"", 0, 0},
		{"hello", 1, 2},
		{"hello world this is a test of the token estimator", 10, 15},
		{strings.Repeat("a", 350), 99, 101},    // 350 / 3.5 = 100
		{strings.Repeat("x", 3500), 999, 1001}, // 3500 / 3.5 = 1000
	}

	for _, tc := range tests {
		got := estimateTokens(tc.text)
		Expect(got).To(BeNumerically(">=", tc.wantMin), "estimateTokens(%d chars) = %d, want [%d, %d]", len(tc.text), got, tc.wantMin, tc.wantMax)
		Expect(got).To(BeNumerically("<=", tc.wantMax), "estimateTokens(%d chars) = %d, want [%d, %d]", len(tc.text), got, tc.wantMin, tc.wantMax)
	}
})
var _ = It("render compact pack projection empty quality", func() {
	result := PackResult{
		PackID:  "pack-empty-quality",
		Fresh:   true,
		Quality: PackQuality{}, // all zero values
		CandidateFiles: []PackFile{
			{Path: "pkg/x.go", Role: "source", Reason: "only match"},
		},
	}

	p := NewCompactPackProjection(result)
	out, _ := RenderCompactPackProjection(p, 0)
	Expect(

		// fresh must be rendered
		strings.Contains(out, "fresh: true")).To(BeTrue(), "expected fresh: true in output")
	Expect(

		// Quality section should still render (with unknown confidence)
		strings.Contains(out, "packet_quality:")).To(BeTrue(), "expected packet_quality section even with empty quality")
	Expect(strings.Contains(out, "localization_confidence: unknown")).To(BeTrue(), "expected unknown localization_confidence for empty quality")
	Expect(strings.Contains(out, "source_owner_found: false")).To(BeTrue(), "expected source_owner_found: false for empty quality")
})
var _ = It("new compact pack projection empty result", func() {
	result := PackResult{}
	p := NewCompactPackProjection(result)
	Expect(p.PackID).To(BeEquivalentTo(""), "expected empty PackID, got %q", p.PackID)
	Expect(p.TotalCandidates).To(BeEquivalentTo(0), "expected 0 TotalCandidates, got %d", p.TotalCandidates)
	Expect(len(p.CandidateFiles)).To(BeEquivalentTo(0), "expected 0 CandidateFiles, got %d", len(p.CandidateFiles))
	Expect(len(p.CandidateSymbols)).To(BeEquivalentTo(0), "expected 0 CandidateSymbols, got %d", len(p.CandidateSymbols))
	Expect(len(p.Tests)).To(BeEquivalentTo(0), "expected 0 Tests, got %d", len(p.Tests))
})
var _ = It("render compact pack projection no candidates", func() {
	result := PackResult{
		PackID: "pack-no-candidates",
		Fresh:  false,
		Quality: PackQuality{
			LocalizationConfidence: "low",
			Reasons:                []string{"no_query_token_overlap_in_packet"},
		},
		UnresolvedGaps: []string{"no_graph_matches"},
	}

	p := NewCompactPackProjection(result)
	out, _ := RenderCompactPackProjection(p, 0)
	Expect(

		// fresh field
		strings.Contains(out, "fresh: false")).To(BeTrue(), "expected fresh: false in output")
	Expect(

		// Still has quality, pack_id, gaps, and evidence_note
		strings.Contains(out, "pack_id: pack-no-candidates")).To(BeTrue(), "expected pack_id in output")
	Expect(strings.Contains(out, "packet_quality:")).To(BeTrue(), "expected packet_quality in output")
	Expect(strings.Contains(out, "context_gaps:")).To(BeTrue(), "expected context_gaps in output")
	Expect(strings.Contains(out, "evidence_note:")).To(BeTrue(), "expected evidence_note in output")
	Expect(

		// No candidate_files, candidate_symbols, tests sections
		strings.Contains(out, "candidate_files:")).To(BeFalse(), "unexpected candidate_files section with no files")
	Expect(strings.Contains(out, "candidate_symbols:")).To(BeFalse(), "unexpected candidate_symbols section with no symbols")
	Expect(strings.Contains(out, "tests:")).To(BeFalse(), "unexpected tests section with no tests")
})
var _ = It("render compact pack projection small budget", func() {
	result := PackResult{
		PackID: "small-budget",
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
		},
		CandidateFiles: []PackFile{
			{Path: "a.go", Role: "source", Reason: "x"},
			{Path: "b.go", Role: "source", Reason: "x"},
			{Path: "c.go", Role: "source", Reason: "x"},
		},
		CandidateSymbols: []PackSymbol{
			{QualifiedName: "F", Path: "a.go", Kind: "func", StartLine: 1},
			{QualifiedName: "G", Path: "b.go", Kind: "func", StartLine: 1},
		},
		Tests: []PackTest{
			{Command: "go test", Path: "a_test.go", Symbol: "TF"},
		},
	}

	p := NewCompactPackProjection(result)
	// Budget of 100 tokens — small enough to force truncation of 6 items
	// but large enough for header + evidence + reserve (≈94 tokens).
	out, returned := RenderCompactPackProjection(p, 100)

	// Output must be under budget
	est := estimateTokens(out)
	Expect(est > 100).To(BeFalse(), "estimateTokens(output) = %d, must be <= budgetTokens=%d", est, 100)
	// Must have quality and pack_id.
	Expect(out).To(ContainSubstring("pack_id:"), "expected pack_id with small budget")
	Expect(out).To(ContainSubstring("packet_quality:"), "expected packet_quality with small budget")

	// Omitted section should be present (100 tokens is tight for 6 items).
	Expect(out).To(ContainSubstring("omitted:"), "expected omitted section with small budget and multiple items")

	// At least some omitted counts on returned struct.
	Expect([]int{returned.OmittedFiles, returned.OmittedSymbols, returned.OmittedTests}).NotTo(
		Equal([]int{0, 0, 0}),
		"expected at least one omitted count > 0 with BudgetTokens=100 and 6 items",
	)
})
var _ = It("render compact pack projection tiny budget header exceeds", func() {
	// Budget so small that even the mandatory header exceeds it.
	// Per contract Section 4.2.3: "if the mandatory header itself exceeds
	// budget, return the mandatory header plus a truncation note and do
	// not add optional candidates." The output MAY exceed budget in this
	// edge case — the header is mandatory.
	result := PackResult{
		PackID: "tiny-budget-pack",
		Fresh:  true,
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"pkg/a.go", "pkg/b.go"},
			Reasons:                []string{"match", "source_owner"},
		},
		CandidateFiles: []PackFile{
			{Path: "pkg/a.go", Role: "source", Reason: "top match"},
		},
		CandidateSymbols: []PackSymbol{
			{QualifiedName: "F", Path: "pkg/a.go", Kind: "func", StartLine: 1},
		},
	}

	p := NewCompactPackProjection(result)
	// Budget of 5 tokens — far less than the header itself.
	out, returned := RenderCompactPackProjection(p, 5)
	Expect(

		// Must still have the mandatory header (pack_id, fresh, quality)
		strings.Contains(out, "context_pack_summary:")).To(BeTrue(), "expected context_pack_summary in output even when header exceeds budget")
	Expect(

		// Must have truncation note
		strings.Contains(out, "truncation_note:")).To(BeTrue(), "expected truncation_note when header exceeds budget")
	Expect(

		// All items omitted on returned struct
		returned.OmittedFiles).To(BeEquivalentTo(1), "expected OmittedFiles=1, got %d", returned.OmittedFiles)
	Expect(returned.OmittedSymbols).To(BeEquivalentTo(1), "expected OmittedSymbols=1, got %d", returned.OmittedSymbols)
	Expect(

		// evidence_note must still be present
		strings.Contains(out, "evidence_note:")).To(BeTrue(), "expected evidence_note even when header exceeds budget")
	Expect(

		// No candidate sections rendered
		strings.Contains(out, "candidate_files:")).To(BeFalse(), "unexpected candidate_files section when header exceeds budget")
})
var _ = It("render compact pack projection symbol renders reason", func() {
	result := PackResult{
		PackID: "symbol-reason-test",
		Fresh:  true,
		Quality: PackQuality{
			LocalizationConfidence: "medium",
		},
		CandidateSymbols: []PackSymbol{
			{QualifiedName: "DoBar", Path: "pkg/bar.go", Kind: "func", StartLine: 20, Reason: "matches query pattern"},
		},
	}

	p := NewCompactPackProjection(result)
	out, _ := RenderCompactPackProjection(p, 0)
	Expect(

		// Symbol reason must appear in output
		strings.Contains(out, "matches query pattern")).To(BeTrue(), "expected symbol reason in output")
	Expect(

		// Symbol line should include both the name and the reason
		strings.Contains(out, "DoBar")).To(BeTrue(), "expected symbol name DoBar in output")
})
var _ = It("render compact pack projection budget 100", func() {
	// Acceptance gate: budget=100 must produce output ≤ 100 tokens.
	files := make([]PackFile, 20)
	for i := range files {
		files[i] = PackFile{
			Path:   "pkg/f" + strings.Repeat("x", 30) + ".go",
			Role:   "source",
			Reason: "relevant to task",
		}
	}

	symbols := make([]PackSymbol, 10)
	for i := range symbols {
		symbols[i] = PackSymbol{
			QualifiedName: "S" + strings.Repeat("y", 25),
			Path:          "pkg/f.go",
			Kind:          "func",
			StartLine:     i * 5,
			Reason:        "match",
		}
	}

	result := PackResult{
		PackID: "budget-100-test",
		Fresh:  true,
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
		},
		CandidateFiles:   files,
		CandidateSymbols: symbols,
		BudgetTokens:     100,
	}

	p := NewCompactPackProjection(result)
	out, returned := RenderCompactPackProjection(p, 100)

	// Hard cap: output must be ≤ budget
	est := estimateTokens(out)
	Expect(est > 100).To(BeFalse(), "estimateTokens(output) = %d, must be <= budgetTokens=%d", est, 100)
	// At least one omitted count must be nonzero (100 tokens is tiny for 30 items).
	Expect([]int{returned.OmittedFiles, returned.OmittedSymbols}).NotTo(
		Equal([]int{0, 0}),
		"expected omitted counts > 0 with budget=100 and 30 items",
	)
})

// TestPackResultCompactProjectionPopulated verifies that the
// Pack()-style wiring (NewCompactPackProjection followed by
// RenderCompactPackProjection) correctly populates CompactProjection
// on a PackResult, matching the logic added to Pack() in Atom 5.
var _ = It("pack result compact projection populated", func() {
	result := PackResult{
		PackID:       "wired-pack-1",
		Fresh:        true,
		BudgetTokens: 80, // small enough to force omission with 10 items
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
		},
		CandidateFiles: []PackFile{
			{Path: "pkg/alpha_service.go", Role: "source", Reason: "top match for the task query"},
			{Path: "pkg/beta_handler.go", Role: "source", Reason: "related handler"},
			{Path: "pkg/gamma_client.go", Role: "source", Reason: "client logic"},
			{Path: "pkg/delta_store.go", Role: "source", Reason: "data store"},
			{Path: "pkg/epsilon_util.go", Role: "source", Reason: "utility functions"},
		},
		CandidateSymbols: []PackSymbol{
			{QualifiedName: "AlphaService.Process", Path: "pkg/alpha_service.go", Kind: "method", StartLine: 42, Reason: "primary handler"},
			{QualifiedName: "BetaHandler.Serve", Path: "pkg/beta_handler.go", Kind: "method", StartLine: 10, Reason: "request handler"},
			{QualifiedName: "GammaClient.Connect", Path: "pkg/gamma_client.go", Kind: "method", StartLine: 20, Reason: "connection logic"},
		},
		Tests: []PackTest{
			{Command: "go test ./pkg/...", Path: "pkg/alpha_service_test.go", Symbol: "TestProcess"},
			{Command: "go test ./pkg/...", Path: "pkg/beta_handler_test.go", Symbol: "TestServe"},
		},
	}

	// Simulate the Pack() wiring from Atom 5.
	proj := NewCompactPackProjection(result)
	_, compactProj := RenderCompactPackProjection(proj, result.BudgetTokens)

	result.CompactProjection = compactProj
	Expect(

		// CompactProjection must be populated.
		result.CompactProjection.PackID).To(BeEquivalentTo("wired-pack-1"), "expected CompactProjection.PackID 'wired-pack-1', got %q", result.CompactProjection.PackID)
	Expect(result.CompactProjection.BudgetTokens).To(BeEquivalentTo(80), "expected CompactProjection.BudgetTokens 80, got %d", result.CompactProjection.BudgetTokens)
	Expect(result.CompactProjection.TotalCandidates).NotTo(BeEquivalentTo(0), "expected TotalCandidates > 0")

	// With BudgetTokens=80 and 10 items with descriptive reasons,
	// at least some items should be omitted.
	totalOmitted := result.CompactProjection.OmittedFiles +
		result.CompactProjection.OmittedSymbols +
		result.CompactProjection.OmittedTests
	Expect(totalOmitted).NotTo(BeEquivalentTo(0), "expected at least one omitted count > 0 with BudgetTokens=80 and 10 items; header+tokens used=%d",
		estimateTokens(strings.Repeat("x", 80*3)))
	Expect(

		// Quality must be correct.
		result.CompactProjection.Quality.LocalizationConfidence).To(BeEquivalentTo("high"), "expected localization_confidence 'high', got %q",
		result.CompactProjection.Quality.LocalizationConfidence)
	Expect(result.CompactProjection.Quality.SourceOwnerFound).To(BeTrue(), "expected SourceOwnerFound=true")
})

// TestPackResultCompactProjectionUnbounded verifies that when
// BudgetTokens=0 (unbounded), the CompactProjection has zero omitted
// counts — matching the contract that BudgetTokens=0 means no cap.
var _ = It("pack result compact projection unbounded", func() {
	files := make([]PackFile, 15)
	for i := range files {
		files[i] = PackFile{Path: "pkg/f.go", Role: "source", Reason: "match"}
	}

	result := PackResult{
		PackID:           "wired-pack-unbounded",
		Fresh:            true,
		Quality:          PackQuality{LocalizationConfidence: "medium"},
		CandidateFiles:   files,
		CandidateSymbols: []PackSymbol{{QualifiedName: "F", Path: "pkg/f.go", Kind: "func"}},
	}

	// Simulate Pack() wiring with BudgetTokens=0 (unbounded).
	proj := NewCompactPackProjection(result)
	_, compactProj := RenderCompactPackProjection(proj, 0)

	result.CompactProjection = compactProj
	Expect(

		// All omitted counts must be zero (no budget enforcement).
		result.CompactProjection.OmittedFiles).To(BeEquivalentTo(0), "expected OmittedFiles=0 in unbounded mode, got %d",
		result.CompactProjection.OmittedFiles)
	Expect(result.CompactProjection.OmittedSymbols).To(BeEquivalentTo(0), "expected OmittedSymbols=0 in unbounded mode, got %d",
		result.CompactProjection.OmittedSymbols)
	Expect(result.CompactProjection.OmittedTests).To(BeEquivalentTo(0), "expected OmittedTests=0 in unbounded mode, got %d",
		result.CompactProjection.OmittedTests)
	Expect(

		// TotalCandidates should reflect actual counts.
		result.CompactProjection.TotalCandidates).To(BeEquivalentTo(16), // 15 files + 1 symbol
		"expected TotalCandidates 16, got %d",
		result.CompactProjection.TotalCandidates)
})

// TestLoadCachedPackCompactProjectionFallback verifies that
// loadCachedPack's zero-value detection works: if CompactProjection
// is not populated (pre-Atom-5 cached pack), it is recomputed.
var _ = It("load cached pack compact projection fallback", func() {
	// Simulate a pre-Atom-5 cached pack: CompactProjection is zero-value.
	result := PackResult{
		PackID:       "legacy-pack",
		Fresh:        true,
		BudgetTokens: 100,
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
		},
		CandidateFiles: []PackFile{
			{Path: "pkg/x.go", Role: "source", Reason: "match"},
			{Path: "pkg/y.go", Role: "source", Reason: "match"},
			{Path: "pkg/z.go", Role: "source", Reason: "match"},
		},
	}
	Expect(

		// Verify CompactProjection is initially zero.
		result.CompactProjection.PackID).To(BeEquivalentTo(""), "expected zero CompactProjection for legacy pack fixture")

	// Simulate the loadCachedPack fallback logic.
	if result.CompactProjection.PackID == "" {
		cp := NewCompactPackProjection(result)
		_, compactProj := RenderCompactPackProjection(cp, result.BudgetTokens)

		result.CompactProjection = compactProj
	}
	Expect(

		// Now CompactProjection must be populated.
		result.CompactProjection.PackID).To(BeEquivalentTo("legacy-pack"), "expected CompactProjection.PackID 'legacy-pack' after fallback, got %q",
		result.CompactProjection.PackID)

	// With BudgetTokens=100 and 3 items, at least one omitted.
	totalOmitted := result.CompactProjection.OmittedFiles +
		result.CompactProjection.OmittedSymbols +
		result.CompactProjection.OmittedTests
	Expect(totalOmitted).NotTo(BeEquivalentTo(0), "expected omitted counts > 0 after fallback with BudgetTokens=100")
})

// TestEstimatedFullTokensSaved_BudgetEnforcement verifies that
// RenderCompactPackProjection records estimated token savings when
// budget enforcement omits items (Atom 6).
var _ = It("estimated full tokens saved budget enforcement", func() {
	resetViewCacheCounters()

	fullSaved, _ := TokenSavingsCounters()
	Expect(fullSaved).To(BeEquivalentTo(0), "expected zero initial savings, got %d", fullSaved)

	// Create a pack with enough items that budget=200 forces omissions
	// but is large enough that the truncated output is genuinely smaller
	// than the full unbounded output.
	files := make([]PackFile, 15)
	for i := range files {
		files[i] = PackFile{Path: "pkg/f" + strings.Repeat("x", 30) + ".go", Role: "source", Reason: "relevant to task"}
	}

	result := PackResult{
		PackID:         "savings-test",
		Fresh:          true,
		Quality:        PackQuality{LocalizationConfidence: "high", SourceOwnerFound: true},
		CandidateFiles: files,
		BudgetTokens:   200,
	}
	proj := NewCompactPackProjection(result)
	out, returned := RenderCompactPackProjection(proj, 200)
	// Budget enforcement: at least some items should be omitted.
	Expect([]int{returned.OmittedFiles, returned.OmittedSymbols, returned.OmittedTests}).NotTo(
		Equal([]int{0, 0, 0}),
		"expected at least some omitted items with budget=200 and 15 files",
	)

	GinkgoWriter.Printf("output tokens=%d, omitted files=%d", estimateTokens(out), returned.OmittedFiles)

	// Savings counter must be > 0 (genuine savings from compact projection).
	fullSaved, _ = TokenSavingsCounters()
	Expect(fullSaved <= 0).To(BeFalse(), "expected estimatedFullTokensSaved > 0 after budget enforcement, got %d", fullSaved)

	GinkgoWriter.Printf("estimatedFullTokensSaved=%d from budget=200 render", fullSaved)
})

// TestEstimatedFullTokensSaved_UnboundedNoSavings verifies that
// unbounded rendering (BudgetTokens=0) does not record false savings.
var _ = It("estimated full tokens saved unbounded no savings", func() {
	resetViewCacheCounters()

	result := PackResult{
		PackID:  "unbounded-savings-test",
		Fresh:   true,
		Quality: PackQuality{LocalizationConfidence: "medium"},
		CandidateFiles: []PackFile{
			{Path: "pkg/a.go", Role: "source", Reason: "match"},
			{Path: "pkg/b.go", Role: "source", Reason: "related"},
		},
	}
	proj := NewCompactPackProjection(result)
	RenderCompactPackProjection(proj, 0) // unbounded

	fullSaved, _ := TokenSavingsCounters()
	Expect(fullSaved).To(BeEquivalentTo(0), "expected zero savings for unbounded render, got %d", fullSaved)
})

// TestEstimatedFullTokensSaved_HeaderExceedsBudget verifies that the
// counter records savings when the header alone exceeds budget
// (edge case: all items omitted). Uses enough candidates that the
// full unbounded output genuinely exceeds the truncated output
// (truncation note costs less than rendering all omitted items).
var _ = It("estimated full tokens saved header exceeds budget", func() {
	resetViewCacheCounters()

	// Many files so full unbounded rendering >> truncated output.
	files := make([]PackFile, 20)
	for i := range files {
		files[i] = PackFile{Path: "pkg/f" + strings.Repeat("x", 40) + ".go", Role: "source", Reason: "relevant to the task query"}
	}

	// Tiny budget: 15 tokens, header alone should be ~50+ tokens.
	result := PackResult{
		PackID: "header-exceeds-test",
		Fresh:  true,
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"internal/foo/bar.go"},
		},
		CandidateFiles: files,
		BudgetTokens:   15,
	}
	proj := NewCompactPackProjection(result)
	out, returned := RenderCompactPackProjection(proj, 15)
	Expect(

		// All items should be omitted (header exceeds budget).
		returned.OmittedFiles).To(BeEquivalentTo(20), "expected all 20 files omitted when header exceeds budget, got OmittedFiles=%d", returned.OmittedFiles)
	Expect(strings.Contains(out, "truncation_note: budget exhausted by header")).To(BeTrue(), "expected truncation note in output")

	// Savings counter should record the delta: full output (header +
	// 20 files + evidence) >> truncated output (header + truncation +
	// evidence), so savings > 0.
	fullSaved, _ := TokenSavingsCounters()
	Expect(fullSaved <= 0).To(BeFalse(), "expected estimatedFullTokensSaved > 0 when header exceeds budget with many files, got %d", fullSaved)

	GinkgoWriter.Printf("estimatedFullTokensSaved=%d from header-exceeds-budget render", fullSaved)
})
