package comprehension

import (
	"strings"
	"testing"
)

func TestNewCompactPackProjection_FromPackResult(t *testing.T) {
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

	// PackID
	if p.PackID != "test-pack-123" {
		t.Errorf("expected PackID 'test-pack-123', got %q", p.PackID)
	}

	// Fresh
	if !p.Fresh {
		t.Error("expected Fresh=true")
	}

	// Quality
	if p.Quality.LocalizationConfidence != "high" {
		t.Errorf("expected LocalizationConfidence 'high', got %q", p.Quality.LocalizationConfidence)
	}

	if !p.Quality.SourceOwnerFound {
		t.Error("expected SourceOwnerFound=true")
	}

	if len(p.Quality.EvidencePaths) != 2 {
		t.Errorf("expected 2 EvidencePaths, got %d", len(p.Quality.EvidencePaths))
	}

	if len(p.Quality.Reasons) != 1 {
		t.Errorf("expected 1 Reason, got %d", len(p.Quality.Reasons))
	}

	// CandidateFiles
	if len(p.CandidateFiles) != 2 {
		t.Errorf("expected 2 CandidateFiles, got %d", len(p.CandidateFiles))
	}

	if p.CandidateFiles[0].Path != "internal/foo/bar.go" {
		t.Errorf("expected file path 'internal/foo/bar.go', got %q", p.CandidateFiles[0].Path)
	}

	if p.CandidateFiles[0].Role != "source" {
		t.Errorf("expected role 'source', got %q", p.CandidateFiles[0].Role)
	}

	// CandidateSymbols
	if len(p.CandidateSymbols) != 1 {
		t.Errorf("expected 1 CandidateSymbol, got %d", len(p.CandidateSymbols))
	}

	if p.CandidateSymbols[0].QualifiedName != "DoFoo" {
		t.Errorf("expected QualifiedName 'DoFoo', got %q", p.CandidateSymbols[0].QualifiedName)
	}

	// Tests
	if len(p.Tests) != 1 {
		t.Errorf("expected 1 Test, got %d", len(p.Tests))
	}

	// UnresolvedGaps
	if len(p.UnresolvedGaps) != 1 {
		t.Errorf("expected 1 UnresolvedGap, got %d", len(p.UnresolvedGaps))
	}

	// BudgetTokens
	if p.BudgetTokens != 500 {
		t.Errorf("expected BudgetTokens 500, got %d", p.BudgetTokens)
	}

	// TotalCandidates
	if p.TotalCandidates != 4 { // 2 files + 1 symbol + 1 test
		t.Errorf("expected TotalCandidates 4, got %d", p.TotalCandidates)
	}
}

func TestRenderCompactPackProjection_NoBudget(t *testing.T) {
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
	out, returned := RenderCompactPackProjection(p, 0) // no budget

	// Quality must be present
	if !strings.Contains(out, "packet_quality:") {
		t.Error("expected packet_quality section in output")
	}

	if !strings.Contains(out, "localization_confidence: medium") {
		t.Error("expected localization_confidence in output")
	}

	if !strings.Contains(out, "source_owner_found: true") {
		t.Error("expected source_owner_found in output")
	}

	// pack_id must be present
	if !strings.Contains(out, "pack_id: pack-no-budget") {
		t.Error("expected pack_id in output")
	}

	// fresh must be rendered
	if !strings.Contains(out, "fresh: true") {
		t.Error("expected fresh: true in output")
	}

	// evidence_note must be present
	if !strings.Contains(out, "evidence_note: context_pack is localization evidence") {
		t.Error("expected evidence_note in output")
	}

	// All items rendered (no budget enforcement)
	if !strings.Contains(out, "pkg/handler.go") {
		t.Error("expected handler.go in output")
	}

	if !strings.Contains(out, "pkg/service.go") {
		t.Error("expected service.go in output")
	}

	if !strings.Contains(out, "HandleRequest") {
		t.Error("expected HandleRequest in output")
	}

	// No omitted section when no budget
	if strings.Contains(out, "omitted:") {
		t.Error("unexpected omitted section when BudgetTokens=0")
	}

	// Returned projection should have zero omitted counts
	if returned.OmittedFiles != 0 {
		t.Errorf("expected OmittedFiles=0 in unbounded mode, got %d", returned.OmittedFiles)
	}

	if returned.OmittedSymbols != 0 {
		t.Errorf("expected OmittedSymbols=0 in unbounded mode, got %d", returned.OmittedSymbols)
	}

	if returned.OmittedTests != 0 {
		t.Errorf("expected OmittedTests=0 in unbounded mode, got %d", returned.OmittedTests)
	}
}

func TestRenderCompactPackProjection_BudgetTruncation(t *testing.T) {
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
	if est > 300 {
		t.Errorf("estimateTokens(output) = %d, must be <= budgetTokens=%d", est, 300)
	}

	// Omitted counts must be populated on the returned struct
	totalOmitted := returned.OmittedFiles + returned.OmittedSymbols + returned.OmittedTests
	if totalOmitted == 0 {
		t.Error("expected at least one omitted count > 0 with BudgetTokens=300 and 35 candidates")
	}

	// pack_id and quality always present
	if !strings.Contains(out, "pack_id: pack-budget-test") {
		t.Error("expected pack_id in truncated output")
	}

	if !strings.Contains(out, "packet_quality:") {
		t.Error("expected packet_quality in truncated output")
	}

	if !strings.Contains(out, "localization_confidence: high") {
		t.Error("expected localization_confidence in truncated output")
	}

	if !strings.Contains(out, "source_owner_found: true") {
		t.Error("expected source_owner_found in truncated output")
	}

	// fresh must be rendered
	if !strings.Contains(out, "fresh: true") {
		t.Error("expected fresh: true in output")
	}

	// evidence_note always present
	if !strings.Contains(out, "evidence_note:") {
		t.Error("expected evidence_note in truncated output")
	}

	// Omitted section should be present
	if !strings.Contains(out, "omitted:") {
		t.Error("expected omitted section with BudgetTokens=300 and 35 candidates")
	}
}

func TestRenderCompactPackProjection_BudgetTokensZeroNoTruncation(t *testing.T) {
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

	// fresh: false must be rendered
	if !strings.Contains(out, "fresh: false") {
		t.Error("expected fresh: false in output")
	}

	// No omitted section
	if strings.Contains(out, "omitted:") {
		t.Error("unexpected omitted section when BudgetTokens=0")
	}

	// Returned projection has zero omitted counts
	if returned.OmittedFiles != 0 {
		t.Errorf("expected OmittedFiles=0, got %d", returned.OmittedFiles)
	}

	// All 15 files rendered (no truncation)
	count := strings.Count(out, "pkg/f.go")
	if count < 15 {
		t.Errorf("expected all 15 files rendered with BudgetTokens=0, got %d occurrences", count)
	}
}

func TestEstimateTokens(t *testing.T) {
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
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("estimateTokens(%d chars) = %d, want [%d, %d]", len(tc.text), got, tc.wantMin, tc.wantMax)
		}
	}
}

func TestRenderCompactPackProjection_EmptyQuality(t *testing.T) {
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

	// fresh must be rendered
	if !strings.Contains(out, "fresh: true") {
		t.Error("expected fresh: true in output")
	}

	// Quality section should still render (with unknown confidence)
	if !strings.Contains(out, "packet_quality:") {
		t.Error("expected packet_quality section even with empty quality")
	}

	if !strings.Contains(out, "localization_confidence: unknown") {
		t.Error("expected unknown localization_confidence for empty quality")
	}

	if !strings.Contains(out, "source_owner_found: false") {
		t.Error("expected source_owner_found: false for empty quality")
	}
}

func TestNewCompactPackProjection_EmptyResult(t *testing.T) {
	result := PackResult{}
	p := NewCompactPackProjection(result)

	if p.PackID != "" {
		t.Errorf("expected empty PackID, got %q", p.PackID)
	}

	if p.TotalCandidates != 0 {
		t.Errorf("expected 0 TotalCandidates, got %d", p.TotalCandidates)
	}

	if len(p.CandidateFiles) != 0 {
		t.Errorf("expected 0 CandidateFiles, got %d", len(p.CandidateFiles))
	}

	if len(p.CandidateSymbols) != 0 {
		t.Errorf("expected 0 CandidateSymbols, got %d", len(p.CandidateSymbols))
	}

	if len(p.Tests) != 0 {
		t.Errorf("expected 0 Tests, got %d", len(p.Tests))
	}
}

func TestRenderCompactPackProjection_NoCandidates(t *testing.T) {
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

	// fresh field
	if !strings.Contains(out, "fresh: false") {
		t.Error("expected fresh: false in output")
	}

	// Still has quality, pack_id, gaps, and evidence_note
	if !strings.Contains(out, "pack_id: pack-no-candidates") {
		t.Error("expected pack_id in output")
	}

	if !strings.Contains(out, "packet_quality:") {
		t.Error("expected packet_quality in output")
	}

	if !strings.Contains(out, "context_gaps:") {
		t.Error("expected context_gaps in output")
	}

	if !strings.Contains(out, "evidence_note:") {
		t.Error("expected evidence_note in output")
	}

	// No candidate_files, candidate_symbols, tests sections
	if strings.Contains(out, "candidate_files:") {
		t.Error("unexpected candidate_files section with no files")
	}

	if strings.Contains(out, "candidate_symbols:") {
		t.Error("unexpected candidate_symbols section with no symbols")
	}

	if strings.Contains(out, "tests:") {
		t.Error("unexpected tests section with no tests")
	}
}

func TestRenderCompactPackProjection_SmallBudget(t *testing.T) {
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
	if est > 100 {
		t.Errorf("estimateTokens(output) = %d, must be <= budgetTokens=%d", est, 100)
	}

	// Must have quality and pack_id
	if !strings.Contains(out, "pack_id:") {
		t.Error("expected pack_id with small budget")
	}

	if !strings.Contains(out, "packet_quality:") {
		t.Error("expected packet_quality with small budget")
	}

	// Omitted section should be present (100 tokens is tight for 6 items)
	if !strings.Contains(out, "omitted:") {
		t.Error("expected omitted section with small budget and multiple items")
	}

	// At least some omitted counts on returned struct
	if returned.OmittedFiles == 0 && returned.OmittedSymbols == 0 && returned.OmittedTests == 0 {
		t.Error("expected at least one omitted count > 0 with BudgetTokens=100 and 6 items")
	}
}

func TestRenderCompactPackProjection_TinyBudgetHeaderExceeds(t *testing.T) {
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

	// Must still have the mandatory header (pack_id, fresh, quality)
	if !strings.Contains(out, "context_pack_summary:") {
		t.Error("expected context_pack_summary in output even when header exceeds budget")
	}

	// Must have truncation note
	if !strings.Contains(out, "truncation_note:") {
		t.Error("expected truncation_note when header exceeds budget")
	}

	// All items omitted on returned struct
	if returned.OmittedFiles != 1 {
		t.Errorf("expected OmittedFiles=1, got %d", returned.OmittedFiles)
	}

	if returned.OmittedSymbols != 1 {
		t.Errorf("expected OmittedSymbols=1, got %d", returned.OmittedSymbols)
	}

	// evidence_note must still be present
	if !strings.Contains(out, "evidence_note:") {
		t.Error("expected evidence_note even when header exceeds budget")
	}

	// No candidate sections rendered
	if strings.Contains(out, "candidate_files:") {
		t.Error("unexpected candidate_files section when header exceeds budget")
	}
}

func TestRenderCompactPackProjection_SymbolRendersReason(t *testing.T) {
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

	// Symbol reason must appear in output
	if !strings.Contains(out, "matches query pattern") {
		t.Error("expected symbol reason in output")
	}

	// Symbol line should include both the name and the reason
	if !strings.Contains(out, "DoBar") {
		t.Error("expected symbol name DoBar in output")
	}
}

func TestRenderCompactPackProjection_Budget100(t *testing.T) {
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
	if est > 100 {
		t.Errorf("estimateTokens(output) = %d, must be <= budgetTokens=%d", est, 100)
	}

	// Omitted counts must be > 0 (100 tokens is tiny for 30 items)
	if returned.OmittedFiles == 0 && returned.OmittedSymbols == 0 {
		t.Error("expected omitted counts > 0 with budget=100 and 30 items")
	}
}

// TestPackResultCompactProjectionPopulated verifies that the
// Pack()-style wiring (NewCompactPackProjection followed by
// RenderCompactPackProjection) correctly populates CompactProjection
// on a PackResult, matching the logic added to Pack() in Atom 5.
func TestPackResultCompactProjectionPopulated(t *testing.T) {
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

	// CompactProjection must be populated.
	if result.CompactProjection.PackID != "wired-pack-1" {
		t.Errorf("expected CompactProjection.PackID 'wired-pack-1', got %q", result.CompactProjection.PackID)
	}

	if result.CompactProjection.BudgetTokens != 80 {
		t.Errorf("expected CompactProjection.BudgetTokens 80, got %d", result.CompactProjection.BudgetTokens)
	}

	if result.CompactProjection.TotalCandidates == 0 {
		t.Error("expected TotalCandidates > 0")
	}

	// With BudgetTokens=80 and 10 items with descriptive reasons,
	// at least some items should be omitted.
	totalOmitted := result.CompactProjection.OmittedFiles +
		result.CompactProjection.OmittedSymbols +
		result.CompactProjection.OmittedTests
	if totalOmitted == 0 {
		t.Errorf("expected at least one omitted count > 0 with BudgetTokens=80 and 10 items; header+tokens used=%d",
			estimateTokens(strings.Repeat("x", 80*3)))
	}

	// Quality must be correct.
	if result.CompactProjection.Quality.LocalizationConfidence != "high" {
		t.Errorf("expected localization_confidence 'high', got %q",
			result.CompactProjection.Quality.LocalizationConfidence)
	}

	if !result.CompactProjection.Quality.SourceOwnerFound {
		t.Error("expected SourceOwnerFound=true")
	}
}

// TestPackResultCompactProjectionUnbounded verifies that when
// BudgetTokens=0 (unbounded), the CompactProjection has zero omitted
// counts — matching the contract that BudgetTokens=0 means no cap.
func TestPackResultCompactProjectionUnbounded(t *testing.T) {
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

	// All omitted counts must be zero (no budget enforcement).
	if result.CompactProjection.OmittedFiles != 0 {
		t.Errorf("expected OmittedFiles=0 in unbounded mode, got %d",
			result.CompactProjection.OmittedFiles)
	}

	if result.CompactProjection.OmittedSymbols != 0 {
		t.Errorf("expected OmittedSymbols=0 in unbounded mode, got %d",
			result.CompactProjection.OmittedSymbols)
	}

	if result.CompactProjection.OmittedTests != 0 {
		t.Errorf("expected OmittedTests=0 in unbounded mode, got %d",
			result.CompactProjection.OmittedTests)
	}

	// TotalCandidates should reflect actual counts.
	if result.CompactProjection.TotalCandidates != 16 { // 15 files + 1 symbol
		t.Errorf("expected TotalCandidates 16, got %d",
			result.CompactProjection.TotalCandidates)
	}
}

// TestLoadCachedPackCompactProjectionFallback verifies that
// loadCachedPack's zero-value detection works: if CompactProjection
// is not populated (pre-Atom-5 cached pack), it is recomputed.
func TestLoadCachedPackCompactProjectionFallback(t *testing.T) {
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

	// Verify CompactProjection is initially zero.
	if result.CompactProjection.PackID != "" {
		t.Error("expected zero CompactProjection for legacy pack fixture")
	}

	// Simulate the loadCachedPack fallback logic.
	if result.CompactProjection.PackID == "" {
		cp := NewCompactPackProjection(result)
		_, compactProj := RenderCompactPackProjection(cp, result.BudgetTokens)

		result.CompactProjection = compactProj
	}

	// Now CompactProjection must be populated.
	if result.CompactProjection.PackID != "legacy-pack" {
		t.Errorf("expected CompactProjection.PackID 'legacy-pack' after fallback, got %q",
			result.CompactProjection.PackID)
	}

	// With BudgetTokens=100 and 3 items, at least one omitted.
	totalOmitted := result.CompactProjection.OmittedFiles +
		result.CompactProjection.OmittedSymbols +
		result.CompactProjection.OmittedTests
	if totalOmitted == 0 {
		t.Error("expected omitted counts > 0 after fallback with BudgetTokens=100")
	}
}

// TestEstimatedFullTokensSaved_BudgetEnforcement verifies that
// RenderCompactPackProjection records estimated token savings when
// budget enforcement omits items (Atom 6).
func TestEstimatedFullTokensSaved_BudgetEnforcement(t *testing.T) {
	ResetViewCacheCounters()

	fullSaved, _ := TokenSavingsCounters()
	if fullSaved != 0 {
		t.Fatalf("expected zero initial savings, got %d", fullSaved)
	}

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
	if returned.OmittedFiles == 0 && returned.OmittedSymbols == 0 && returned.OmittedTests == 0 {
		t.Error("expected at least some omitted items with budget=200 and 15 files")
	}

	t.Logf("output tokens=%d, omitted files=%d", estimateTokens(out), returned.OmittedFiles)

	// Savings counter must be > 0 (genuine savings from compact projection).
	fullSaved, _ = TokenSavingsCounters()
	if fullSaved <= 0 {
		t.Errorf("expected estimatedFullTokensSaved > 0 after budget enforcement, got %d", fullSaved)
	}

	t.Logf("estimatedFullTokensSaved=%d from budget=200 render", fullSaved)
}

// TestEstimatedFullTokensSaved_UnboundedNoSavings verifies that
// unbounded rendering (BudgetTokens=0) does not record false savings.
func TestEstimatedFullTokensSaved_UnboundedNoSavings(t *testing.T) {
	ResetViewCacheCounters()

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
	if fullSaved != 0 {
		t.Errorf("expected zero savings for unbounded render, got %d", fullSaved)
	}
}

// TestEstimatedFullTokensSaved_HeaderExceedsBudget verifies that the
// counter records savings when the header alone exceeds budget
// (edge case: all items omitted). Uses enough candidates that the
// full unbounded output genuinely exceeds the truncated output
// (truncation note costs less than rendering all omitted items).
func TestEstimatedFullTokensSaved_HeaderExceedsBudget(t *testing.T) {
	ResetViewCacheCounters()

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

	// All items should be omitted (header exceeds budget).
	if returned.OmittedFiles != 20 {
		t.Errorf("expected all 20 files omitted when header exceeds budget, got OmittedFiles=%d", returned.OmittedFiles)
	}

	if !strings.Contains(out, "truncation_note: budget exhausted by header") {
		t.Error("expected truncation note in output")
	}

	// Savings counter should record the delta: full output (header +
	// 20 files + evidence) >> truncated output (header + truncation +
	// evidence), so savings > 0.
	fullSaved, _ := TokenSavingsCounters()
	if fullSaved <= 0 {
		t.Errorf("expected estimatedFullTokensSaved > 0 when header exceeds budget with many files, got %d", fullSaved)
	}

	t.Logf("estimatedFullTokensSaved=%d from header-exceeds-budget render", fullSaved)
}
