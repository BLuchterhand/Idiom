package comprehension

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("assess pack quality distinguishes freshness from weak source owner", func() {
	quality := assessPackQuality(
		"chat input text box newline wrapping first line invisible",
		[]PackFile{
			{Path: "internal/evidence/transcript/debuglog.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/evidence/transcript/debuglog.go#DebugLogger.LogContext"}},
			{Path: "internal/surfaces/tui/app/theme_style.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/surfaces/tui/app/theme_style.go#themeText"}},
		},
		[]PackSymbol{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Kind: "test", QualifiedName: "TestInputHeightUsesTextareaWrappedLineCount"},
		},
		[]PackTest{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/surfaces/tui/app"},
		},
	)
	Expect(quality.SourceOwnerFound).To(BeFalse(), "test-only relevance must not claim a source owner: %#v", quality)
	Expect(quality.LocalizationConfidence).To(BeEquivalentTo("low"), "localization confidence = %q, want low: %#v", quality.LocalizationConfidence, quality)
	Expect(len(quality.EvidencePaths)).NotTo(BeEquivalentTo(0), "expected strongest test evidence first, got %#v", quality.EvidencePaths)
	Expect(quality.EvidencePaths[0]).To(BeEquivalentTo("internal/surfaces/tui/app/rendering_test.go"), "expected strongest test evidence first, got %#v", quality.EvidencePaths)
})
var _ = It("assess pack quality treats generic source overlap as weak", func() {
	quality := assessPackQuality(
		"Research only; investigate this TUI bug: the first time the chat input text box wraps to a newline, the first existing line shifts upward and is no longer visible.",
		[]PackFile{
			{Path: "internal/runtime/agent/runner.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/runtime/agent/runner.go#textOnlyChatOptions"}},
			{Path: "internal/runtime/agent/plan_execute_runtime_policy.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/runtime/agent/plan_execute_runtime_policy.go#planExecuteTextualToolParserFromChatOptions"}},
			{Path: "internal/surfaces/tui/app/mocha_style.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/surfaces/tui/app/mocha_style.go#mochaText"}},
		},
		[]PackSymbol{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Kind: "test", QualifiedName: "TestInputHeightUsesTextareaWrappedLineCount", Reason: "matched task terms"},
		},
		[]PackTest{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/surfaces/tui/app"},
		},
	)
	Expect(quality.SourceOwnerFound).To(BeFalse(), "generic chat/text source overlap must not claim source ownership: %#v", quality)
	Expect(quality.LocalizationConfidence).To(BeEquivalentTo("low"), "localization confidence = %q, want low: %#v", quality.LocalizationConfidence, quality)
	Expect(len(quality.EvidencePaths)).NotTo(BeEquivalentTo(0), "expected relevant test evidence to outrank generic source overlap, got %#v", quality.EvidencePaths)
	Expect(quality.EvidencePaths[0]).To(BeEquivalentTo("internal/surfaces/tui/app/rendering_test.go"), "expected relevant test evidence to outrank generic source overlap, got %#v", quality.EvidencePaths)
})
var _ = It("assess pack quality finds source owner", func() {
	quality := assessPackQuality(
		"textarea syncInputHeight input height wraps prompt visible top line",
		[]PackFile{
			{Path: "internal/surfaces/tui/app/model_state.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/surfaces/tui/app/model_state.go#Model.syncInputHeight", "symbol://internal/surfaces/tui/app/model_state.go#Model.desiredInputHeight"}},
			{Path: "internal/surfaces/tui/app/rendering_test.go", Role: "test", Reason: "matched task terms", Handles: []string{"symbol://internal/surfaces/tui/app/rendering_test.go#TestInputHeightUsesTextareaWrappedLineCount"}},
		},
		[]PackSymbol{
			{Path: "internal/surfaces/tui/app/model_state.go", Kind: "method", QualifiedName: "Model.syncInputHeight"},
			{Path: "internal/surfaces/tui/app/model_state.go", Kind: "method", QualifiedName: "Model.desiredInputHeight"},
		},
		[]PackTest{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/surfaces/tui/app"},
		},
	)
	Expect(quality.SourceOwnerFound).To(BeTrue(), "expected source owner, got %#v", quality)
	Expect(quality.LocalizationConfidence).To(BeEquivalentTo("high"), "localization confidence = %q, want high: %#v", quality.LocalizationConfidence, quality)
	Expect(len(quality.EvidencePaths)).NotTo(BeEquivalentTo(0), "expected source owner first, got %#v", quality.EvidencePaths)
	Expect(quality.EvidencePaths[0]).To(BeEquivalentTo("internal/surfaces/tui/app/model_state.go"), "expected source owner first, got %#v", quality.EvidencePaths)
})
