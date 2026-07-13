package llama

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ServerController", func() {
	It("uses the explicit flash-attention mode in llama arguments", func() {
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
		Expect(args).To(ContainElements("--flash-attn", "on"))
		Expect(args).NotTo(ContainElement("-fa"))

		flashIndex := findArgument(args, "--flash-attn")
		Expect(flashIndex).To(BeNumerically(">=", 0))
		Expect(len(args)).To(BeNumerically(">", flashIndex+1))
		Expect(args[flashIndex+1]).To(Equal("on"))
	})

	It("omits llama-only flags from MLX arguments", func() {
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
		for _, forbidden := range []string{"--jinja", "--flash-attn", "--reasoning", "--reasoning-format", "--reasoning-budget", "-c", "-t", "-ngl", "-m"} {
			Expect(args).NotTo(ContainElement(forbidden))
		}
		Expect(args).To(ContainElement("--trust-remote-code"))
		Expect(argumentValue(args, "--model")).To(Equal("mlx-community/Qwen3.6-27B-MLX-4bit"))
		Expect(argumentValue(args, "--host")).To(Equal("127.0.0.1"))
		Expect(argumentValue(args, "--port")).To(Equal("8080"))
	})

	It("fails before spawning when the llama model is missing", func() {
		root := GinkgoT().TempDir()
		controller := NewServerController(config.LlamaServer{
			Binary:  "llama-server-that-should-not-run",
			Model:   filepath.Join(root, "missing.gguf"),
			LogFile: filepath.Join(root, "server.log"),
		}, nil)

		err := controller.Start(context.Background())
		Expect(err).To(MatchError(And(
			ContainSubstring("llama model path"),
			ContainSubstring("missing.gguf"),
		)))
	})

	It("fails before spawning when the log file is missing", func() {
		controller := NewServerController(config.LlamaServer{
			Binary: "llama-server-that-should-not-run",
			Model:  "model.gguf",
		}, nil)

		Expect(controller.Start(context.Background())).To(MatchError(ContainSubstring("server log file is required")))
	})

	It("starts and stops a real commandexec-managed server process", Label("integration"), func() {
		if runtime.GOOS == "windows" {
			Skip("shell trap and interrupt behavior are Unix-specific")
		}

		root := GinkgoT().TempDir()
		binary := filepath.Join(root, "fake-llama-server")
		model := filepath.Join(root, "model.gguf")
		logPath := filepath.Join(root, "logs", "server.log")
		readyPath := filepath.Join(root, "server.ready")

		Expect(os.WriteFile(model, []byte("model"), 0o644)).To(Succeed())

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
		Expect(os.WriteFile(binary, []byte(script), 0o755)).To(Succeed())

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
		DeferCleanup(cancelStart)
		Expect(controller.Start(startCtx)).To(Succeed())
		DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = controller.Stop(ctx)
		})

		waitForFileContent(controller, readyPath, "ready")
		cancelStart()
		time.Sleep(100 * time.Millisecond)
		Expect(controller.IsRunning()).To(BeTrue(), "startup-context cancellation must not own the process lifecycle")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(controller.Stop(ctx)).To(Succeed())
		waitForControllerStopped(controller)

		body, err := os.ReadFile(logPath)
		Expect(err).NotTo(HaveOccurred())
		log := string(body)
		Expect(log).To(And(
			ContainSubstring("-m "+model),
			ContainSubstring("--host 127.0.0.1"),
			ContainSubstring("--port 4321"),
			ContainSubstring("-c 128"),
			ContainSubstring("-t 2"),
			ContainSubstring("--fake-extra"),
			ContainSubstring("--ready-file "+readyPath),
			ContainSubstring("interrupted"),
		))
	})
})

func findArgument(args []string, want string) int {
	for index, argument := range args {
		if argument == want {
			return index
		}
	}

	return -1
}

func argumentValue(args []string, name string) string {
	index := findArgument(args, name)
	if index < 0 || index+1 >= len(args) {
		return ""
	}

	return args[index+1]
}

func waitForFileContent(controller *ServerController, path string, want string) {
	Eventually(func(g Gomega) {
		body, err := os.ReadFile(path)
		if !controller.IsRunning() {
			log, _ := os.ReadFile(controller.LogPath())
			g.Expect(controller.IsRunning()).To(BeTrue(), "server exited while waiting for %q in %s; server_log=%q", want, path, string(log))
		}
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(body)).To(ContainSubstring(want))
	}, 10*time.Second, 10*time.Millisecond).Should(Succeed())
}

func waitForControllerStopped(controller *ServerController) {
	Eventually(controller.IsRunning, 3*time.Second, 10*time.Millisecond).Should(BeFalse())
}
