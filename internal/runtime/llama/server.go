package llama

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/config"
)

type ServerController struct {
	cfg    config.LlamaServer
	client *Client

	mu      sync.Mutex
	process *commandexec.Process
}

func NewServerController(cfg config.LlamaServer, client *Client) *ServerController {
	return &ServerController{
		cfg:    cfg,
		client: client,
	}
}

func (s *ServerController) LogPath() string {
	return s.cfg.LogFile
}

func (s *ServerController) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunningLocked() {
		return nil
	}

	if err := s.validateLaunchConfig(); err != nil {
		return err
	}

	process, err := commandexec.Start(context.Background(), commandexec.Spec{
		Name:          s.cfg.Binary,
		Args:          s.buildArgs(),
		OutputLogPath: s.cfg.LogFile,
	})
	if err != nil {
		return llmServerStartError("start llama-server", err)
	}

	s.process = process

	go func() {
		<-process.Done()

		s.mu.Lock()
		defer s.mu.Unlock()

		if s.process == process {
			s.process = nil
		}
	}()

	return nil
}

func (s *ServerController) validateLaunchConfig() error {
	if strings.TrimSpace(s.cfg.Binary) == "" {
		return llmServerConfigError("server binary is required")
	}
	if strings.TrimSpace(s.cfg.LogFile) == "" {
		return llmServerConfigError("server log file is required")
	}

	model := strings.TrimSpace(s.cfg.Model)
	if model == "" {
		return llmServerConfigError("server model is required")
	}

	if s.cfg.IsMLX() {
		return nil
	}

	if _, err := os.Stat(model); err != nil {
		return llmServerModelUnavailableError(fmt.Sprintf("llama model path %q is not readable", model), err)
	}

	return nil
}

func (s *ServerController) Stop(ctx context.Context) error {
	s.mu.Lock()

	process := s.process
	s.mu.Unlock()

	if process == nil || !process.Alive() {
		return nil
	}

	if err := process.Stop(ctx, os.Interrupt); err != nil {
		return llmServerStopError("stop llama-server", err)
	}

	return nil
}

func (s *ServerController) Restart(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := s.Stop(stopCtx); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		return err
	}

	return s.Start(ctx)
}

func (s *ServerController) WaitUntilReady(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return llmServerStopError("wait for llama-server readiness", ctx.Err())
		case <-ticker.C:
			models, err := s.client.ListModels(ctx)
			if err == nil && len(models) > 0 {
				return nil
			}

			if !s.IsRunning() {
				return llmServerReadinessError("llama-server exited before becoming ready")
			}
		}
	}
}

func (s *ServerController) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.isRunningLocked()
}

func (s *ServerController) buildArgs() []string {
	if s.cfg.IsMLX() {
		return s.buildMLXArgs()
	}

	return s.buildLlamaArgs()
}

func (s *ServerController) buildLlamaArgs() []string {
	args := []string{
		"-m", s.cfg.Model,
		"--host", s.cfg.Host,
		"--port", strconv.Itoa(s.cfg.Port),
		"-c", strconv.Itoa(s.cfg.CtxSize),
		"-t", strconv.Itoa(s.cfg.Threads),
	}
	if s.cfg.GPULayers > 0 {
		args = append(args, "-ngl", strconv.Itoa(s.cfg.GPULayers))
	}

	if s.cfg.Jinja {
		args = append(args, "--jinja")
	}

	if s.cfg.FlashAttention {
		args = append(args, "--flash-attn", "on")
	}

	if s.cfg.Reasoning != "" {
		args = append(args, "--reasoning", s.cfg.Reasoning)
	}

	if s.cfg.ReasoningFormat != "" {
		args = append(args, "--reasoning-format", s.cfg.ReasoningFormat)
	}

	if s.cfg.ReasoningBudget > 0 {
		args = append(args, "--reasoning-budget", strconv.Itoa(s.cfg.ReasoningBudget))
	}
	args = append(args, s.cfg.ExtraArgs...)

	return args
}

// buildMLXArgs builds the argv for `mlx_lm.server`, which exposes an
// OpenAI-compatible HTTP API backed by Apple MLX. mlx_lm.server has no
// equivalents for llama.cpp-specific flags (jinja, flash-attn, reasoning
// mode, cache-type quantization), so those are dropped here. Model is
// treated as a Hugging Face ID or local MLX directory, not a file path.
func (s *ServerController) buildMLXArgs() []string {
	args := []string{
		"--model", s.cfg.Model,
		"--host", s.cfg.Host,
		"--port", strconv.Itoa(s.cfg.Port),
	}

	args = append(args, s.cfg.ExtraArgs...)

	return args
}

func (s *ServerController) isRunningLocked() bool {
	return s.process != nil && s.process.Alive()
}
