package agent

import (
	"encoding/json"
	"testing"

	"idiom/internal/runtime/llama"
)

func TestRunnerMetricsAggregatesProviderCacheUsage(t *testing.T) {
	var metrics runnerMetrics
	metrics.addUsage(llama.Usage{
		PromptTokens:               10,
		CompletionTokens:           2,
		TotalTokens:                12,
		PromptCacheHitTokens:       0,
		PromptCacheMissTokens:      0,
		PromptCacheAccountingKnown: true,
		ReasoningTokens:            1,
	})
	metrics.addUsage(llama.Usage{
		PromptTokens:               20,
		CompletionTokens:           3,
		TotalTokens:                23,
		PromptCacheHitTokens:       15,
		PromptCacheMissTokens:      5,
		PromptCacheAccountingKnown: true,
		ReasoningTokens:            2,
	})

	if metrics.PromptTokens != 30 ||
		metrics.CompletionTokens != 5 ||
		metrics.TotalTokens != 35 ||
		metrics.PromptCacheHitTokens != 15 ||
		metrics.PromptCacheMissTokens != 5 ||
		!metrics.PromptCacheAccountingKnown ||
		metrics.ReasoningTokens != 3 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
}

func TestRunnerBudgetLedgerSerializesProviderCacheUsage(t *testing.T) {
	ledger := runnerBudgetLedger{
		Lane:                       "main",
		UsedModelCalls:             1,
		UsedToolResults:            0,
		PromptTokens:               10,
		CompletionTokens:           2,
		TotalTokens:                12,
		PromptCacheHitTokens:       7,
		PromptCacheMissTokens:      3,
		PromptCacheAccountingKnown: true,
		ReasoningTokens:            1,
	}
	body, err := json.Marshal(ledger)
	if err != nil {
		t.Fatalf("marshal ledger: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal ledger: %v", err)
	}
	assertNumberField(t, got, "prompt_cache_hit_tokens", 7)
	assertNumberField(t, got, "prompt_cache_miss_tokens", 3)
	assertNumberField(t, got, "reasoning_tokens", 1)
	if known, ok := got["prompt_cache_accounting_known"].(bool); !ok || !known {
		t.Fatalf("prompt_cache_accounting_known = %#v, want true", got["prompt_cache_accounting_known"])
	}
}
