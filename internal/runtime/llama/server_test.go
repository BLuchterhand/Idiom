package llama

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestStartReturnsClearErrorWhenLogFileMissing(t *testing.T) {
	controller := NewServerController(config.LlamaServer{
		Binary: "llama-server-that-should-not-run",
		Model:  "model.gguf",
	}, nil)

	err := controller.Start(context.Background())
	if err == nil {
		t.Fatal("expected missing log file to fail before spawning server")
	}

	if !strings.Contains(err.Error(), "server log file is required") {
		t.Fatalf("expected clear missing-log-file error, got %v", err)
	}
}

func TestServerControllerStartStopUsesCommandexecLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell trap and interrupt behavior are Unix-specific")
	}

	root := t.TempDir()
	binary := filepath.Join(root, "fake-llama-server")
	model := filepath.Join(root, "model.gguf")
	logPath := filepath.Join(root, "logs", "server.log")
	readyPath := filepath.Join(root, "server.ready")

	if err := os.WriteFile(model, []byte("model"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}

	script := `#!/bin/sh
trap 'echo interrupted >&2; exit 0' INT
original="$*"
ready_file=""
while [ "$#" -gt 0 ]; do
	if [ "$1" = "--ready-file" ] && [ "$#" -gt 1 ]; then
		shift
		ready_file="$1"
	fi
	shift || break
done
echo "argv:$original"
if [ -n "$ready_file" ]; then
	echo ready > "$ready_file"
fi
while true; do sleep 1; done
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake server: %v", err)
	}

	controller := NewServerController(config.LlamaServer{
		Binary:    binary,
		Model:     model,
		LogFile:   logPath,
		Host:      "127.0.0.1",
		Port:      4321,
		CtxSize:   128,
		Threads:   2,
		ExtraArgs: []string{"--fake-extra", "--ready-file", readyPath},
	}, nil)

	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := controller.Start(startCtx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = controller.Stop(ctx)
	})

	waitForLogContent(t, readyPath, "ready")
	cancelStart()
	time.Sleep(100 * time.Millisecond)

	if !controller.IsRunning() {
		t.Fatal("controller should report running after startup context cancellation")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := controller.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	waitForControllerStopped(t, controller)

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(body)
	for _, want := range []string{"-m " + model, "--host 127.0.0.1", "--port 4321", "-c 128", "-t 2", "--fake-extra", "--ready-file " + readyPath, "interrupted"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q: %q", want, log)
		}
	}
}

func waitForLogContent(t *testing.T, path string, want string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(body), want) {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	body, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %q in %s; content=%q", want, path, string(body))
}

func waitForControllerStopped(t *testing.T, controller *ServerController) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !controller.IsRunning() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("controller still reports running after stop")
}
