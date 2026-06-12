package llama

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
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

func TestBuildArgsForMLXOmitsLlamaFlags(t *testing.T) {
	controller := &ServerController{
		cfg: config.LlamaServer{
			Kind:            config.BackendMLX,
			Model:           "mlx-community/Qwen3.6-27B-MLX-4bit",
			Host:            "127.0.0.1",
			Port:            8080,
			CtxSize:         65536,
			Threads:         8,
			Jinja:           true,
			FlashAttention:  true,
			Reasoning:       "auto",
			ReasoningFormat: "deepseek",
			ReasoningBudget: -1,
			ExtraArgs:       []string{"--trust-remote-code"},
		},
	}

	args := controller.buildArgs()

	forbidden := []string{"--jinja", "--flash-attn", "--reasoning", "--reasoning-format", "--reasoning-budget", "-c", "-t", "-ngl", "-m"}
	for _, arg := range args {
		for _, f := range forbidden {
			if arg == f {
				t.Fatalf("MLX args should not contain llama-only flag %q, got %#v", f, args)
			}
		}
	}

	var sawModel, sawHost, sawPort, sawExtra bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) && args[i+1] == "mlx-community/Qwen3.6-27B-MLX-4bit" {
				sawModel = true
			}
		case "--host":
			if i+1 < len(args) && args[i+1] == "127.0.0.1" {
				sawHost = true
			}
		case "--port":
			if i+1 < len(args) && args[i+1] == "8080" {
				sawPort = true
			}
		case "--trust-remote-code":
			sawExtra = true
		}
	}

	if !sawModel || !sawHost || !sawPort || !sawExtra {
		t.Fatalf("MLX args missing required flags (model=%v host=%v port=%v extra=%v): %#v",
			sawModel, sawHost, sawPort, sawExtra, args)
	}
}

func TestStartReturnsClearErrorWhenLlamaModelMissing(t *testing.T) {
	root := t.TempDir()
	controller := NewServerController(config.LlamaServer{
		Binary:  "llama-server-that-should-not-run",
		Model:   filepath.Join(root, "missing.gguf"),
		LogFile: filepath.Join(root, "server.log"),
	}, nil)

	err := controller.Start(context.Background())
	if err == nil {
		t.Fatal("expected missing model path to fail before spawning server")
	}

	if !strings.Contains(err.Error(), "llama model path") || !strings.Contains(err.Error(), "missing.gguf") {
		t.Fatalf("expected clear missing-model error, got %v", err)
	}
}
