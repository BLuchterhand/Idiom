package workspace

import (
	"testing"
	"time"
)

func TestPlanLLMRerankOffKeepsLimit(t *testing.T) {
	t.Setenv("IDIOM_LLM_RERANK", "")

	if p := planLLMRerank(5); p.enabled || p.packLimit != 5 || p.keep != 5 {
		t.Fatalf("off plan = %+v", p)
	}

	if p := planLLMRerank(0); p.enabled || p.packLimit != 0 || p.keep != 0 {
		t.Fatalf("off plan, no limit = %+v", p)
	}
}

func TestPlanLLMRerankOnWidensPoolAndKeepsCallerLimit(t *testing.T) {
	t.Setenv("IDIOM_LLM_RERANK", "on")
	t.Setenv("IDIOM_RERANK_BACKENDS", "cross")

	if p := planLLMRerank(0); !p.enabled || p.packLimit != 30 || p.keep != defaultImpactLimit {
		t.Fatalf("on plan = %+v", p)
	}

	if p := planLLMRerank(50); p.packLimit != 50 || p.keep != 50 {
		t.Fatalf("on plan, wide limit = %+v", p)
	}
}

func TestToolTimeoutFloorFromEnvOnlyRaises(t *testing.T) {
	t.Setenv(toolTimeoutFloorEnv, "")

	base := ToolTimeoutForRequest(ToolRequest{Tool: ToolNameContextImpact})

	t.Setenv(toolTimeoutFloorEnv, "240")

	if got := ToolTimeoutForRequest(ToolRequest{Tool: ToolNameContextImpact}); got != 240*time.Second {
		t.Fatalf("raised = %v (base %v)", got, base)
	}

	t.Setenv(toolTimeoutFloorEnv, "1")

	if got := ToolTimeoutForRequest(ToolRequest{Tool: ToolNameContextImpact}); got != base {
		t.Fatalf("lowered = %v, base %v", got, base)
	}
}
