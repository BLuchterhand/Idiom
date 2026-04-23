package llama

import (
	"testing"

	"idiom/internal/config"
)

func TestBuildArgsUsesExplicitFlashAttentionMode(t *testing.T) {
	controller := &ServerController{
		cfg: config.LlamaServer{
			Model:           "model.gguf",
			Host:            "127.0.0.1",
			Port:            8080,
			CtxSize:         65536,
			Threads:         8,
			Jinja:           true,
			FlashAttention:  true,
			Reasoning:       "auto",
			ReasoningFormat: "deepseek",
			ReasoningBudget: -1,
		},
	}

	args := controller.buildArgs()
	foundFlashAttention := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--flash-attn" && args[i+1] == "on" {
			foundFlashAttention = true
			break
		}
	}
	if !foundFlashAttention {
		t.Fatalf("expected explicit --flash-attn on args, got %#v", args)
	}
	for _, arg := range args {
		if arg == "-fa" {
			t.Fatalf("did not expect legacy -fa flag in args: %#v", args)
		}
	}
}
