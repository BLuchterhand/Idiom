package comprehension

import "testing"

func TestAssessPackQualityDistinguishesFreshnessFromWeakSourceOwner(t *testing.T) {
	quality := assessPackQuality(
		"chat input text box newline wrapping first line invisible",
		[]PackFile{
			{Path: "internal/transcript/debuglog.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/transcript/debuglog.go#DebugLogger.LogContext"}},
			{Path: "internal/app/theme_style.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/app/theme_style.go#themeText"}},
		},
		[]PackSymbol{
			{Path: "internal/app/rendering_test.go", Kind: "test", QualifiedName: "TestInputHeightUsesTextareaWrappedLineCount"},
		},
		[]PackTest{
			{Path: "internal/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/app"},
		},
	)

	if quality.SourceOwnerFound {
		t.Fatalf("test-only relevance must not claim a source owner: %#v", quality)
	}
	if quality.LocalizationConfidence != "low" {
		t.Fatalf("localization confidence = %q, want low: %#v", quality.LocalizationConfidence, quality)
	}
	if len(quality.EvidencePaths) == 0 || quality.EvidencePaths[0] != "internal/app/rendering_test.go" {
		t.Fatalf("expected strongest test evidence first, got %#v", quality.EvidencePaths)
	}
}

func TestAssessPackQualityTreatsGenericSourceOverlapAsWeak(t *testing.T) {
	quality := assessPackQuality(
		"Research only; investigate this TUI bug: the first time the chat input text box wraps to a newline, the first existing line shifts upward and is no longer visible.",
		[]PackFile{
			{Path: "internal/agent/runner.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/agent/runner.go#textOnlyChatOptions"}},
			{Path: "internal/agent/plan_execute_runtime_policy.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/agent/plan_execute_runtime_policy.go#planExecuteTextualToolParserFromChatOptions"}},
			{Path: "internal/app/mocha_style.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/app/mocha_style.go#mochaText"}},
		},
		[]PackSymbol{
			{Path: "internal/app/rendering_test.go", Kind: "test", QualifiedName: "TestInputHeightUsesTextareaWrappedLineCount", Reason: "matched task terms"},
		},
		[]PackTest{
			{Path: "internal/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/app"},
		},
	)

	if quality.SourceOwnerFound {
		t.Fatalf("generic chat/text source overlap must not claim source ownership: %#v", quality)
	}
	if quality.LocalizationConfidence != "low" {
		t.Fatalf("localization confidence = %q, want low: %#v", quality.LocalizationConfidence, quality)
	}
	if len(quality.EvidencePaths) == 0 || quality.EvidencePaths[0] != "internal/app/rendering_test.go" {
		t.Fatalf("expected relevant test evidence to outrank generic source overlap, got %#v", quality.EvidencePaths)
	}
}

func TestAssessPackQualityFindsSourceOwner(t *testing.T) {
	quality := assessPackQuality(
		"textarea syncInputHeight input height wraps prompt visible top line",
		[]PackFile{
			{Path: "internal/app/model_state.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/app/model_state.go#Model.syncInputHeight", "symbol://internal/app/model_state.go#Model.desiredInputHeight"}},
			{Path: "internal/app/rendering_test.go", Role: "test", Reason: "matched task terms", Handles: []string{"symbol://internal/app/rendering_test.go#TestInputHeightUsesTextareaWrappedLineCount"}},
		},
		[]PackSymbol{
			{Path: "internal/app/model_state.go", Kind: "method", QualifiedName: "Model.syncInputHeight"},
			{Path: "internal/app/model_state.go", Kind: "method", QualifiedName: "Model.desiredInputHeight"},
		},
		[]PackTest{
			{Path: "internal/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/app"},
		},
	)

	if !quality.SourceOwnerFound {
		t.Fatalf("expected source owner, got %#v", quality)
	}
	if quality.LocalizationConfidence != "high" {
		t.Fatalf("localization confidence = %q, want high: %#v", quality.LocalizationConfidence, quality)
	}
	if len(quality.EvidencePaths) == 0 || quality.EvidencePaths[0] != "internal/app/model_state.go" {
		t.Fatalf("expected source owner first, got %#v", quality.EvidencePaths)
	}
}
