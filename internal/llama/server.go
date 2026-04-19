package llama

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"qwen-bubbles/internal/config"
)

type ServerController struct {
	cfg    config.LlamaServer
	client *Client

	mu  sync.Mutex
	cmd *exec.Cmd
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

	if err := os.MkdirAll(filepath.Dir(s.cfg.LogFile), 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	logFile, err := os.OpenFile(s.cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	args := s.buildArgs()
	cmd := exec.Command(s.cfg.Binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start llama-server: %w", err)
	}

	s.cmd = cmd

	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.cmd == cmd {
			s.cmd = nil
		}
	}()

	return nil
}

func (s *ServerController) Stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		if err := cmd.Process.Kill(); err != nil {
			return fmt.Errorf("stop llama-server: %w", err)
		}
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		case <-ticker.C:
			if !processAlive(cmd.Process) {
				return nil
			}
		}
	}
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
			return ctx.Err()
		case <-ticker.C:
			models, err := s.client.ListModels(ctx)
			if err == nil && len(models) > 0 {
				return nil
			}
			if !s.IsRunning() {
				return fmt.Errorf("llama-server exited before becoming ready")
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
	args = append(args, "--reasoning-budget", strconv.Itoa(s.cfg.ReasoningBudget))
	args = append(args, s.cfg.ExtraArgs...)
	return args
}

func (s *ServerController) isRunningLocked() bool {
	return s.cmd != nil && s.cmd.Process != nil && processAlive(s.cmd.Process)
}

func processAlive(p *os.Process) bool {
	if p == nil {
		return false
	}
	err := p.Signal(syscall.Signal(0))
	return err == nil
}
