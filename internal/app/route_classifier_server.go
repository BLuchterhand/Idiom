package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"qwen-bubbles/internal/llama"
)

func (m *Model) routeClassifierClient(timeout time.Duration) *llama.Client {
	if m == nil {
		return nil
	}
	baseURL := m.cfg.RouteClassifierBaseURL()
	if strings.TrimSpace(baseURL) == "" {
		return m.client
	}
	if timeout > 0 {
		return llama.NewClient(baseURL, timeout)
	}
	if m.classifierClient == nil {
		m.classifierClient = llama.NewClient(baseURL, 0)
	}
	return m.classifierClient
}

func (m *Model) ensureRouteClassifierReady(ctx context.Context) error {
	if m == nil || !m.cfg.RouteClassifier.Enabled || !m.cfg.RouteClassifier.AutoStartServer {
		return nil
	}
	baseURL := m.cfg.RouteClassifierBaseURL()
	if strings.TrimSpace(baseURL) == "" {
		return nil
	}
	client := m.routeClassifierClient(0)
	if routeClassifierHealthy(ctx, client) {
		return nil
	}

	m.routeClassifierStartMu.Lock()
	defer m.routeClassifierStartMu.Unlock()

	client = m.routeClassifierClient(0)
	if routeClassifierHealthy(ctx, client) {
		return nil
	}
	serverCfg := m.cfg.RouteClassifierServerConfig()
	if err := validateRouteClassifierBinary(serverCfg.Binary); err != nil {
		return err
	}
	if strings.TrimSpace(serverCfg.Model) != "" {
		if info, err := os.Stat(serverCfg.Model); err != nil {
			return fmt.Errorf("route classifier model is not readable at %s: %w", serverCfg.Model, err)
		} else if info.IsDir() {
			return fmt.Errorf("route classifier model path is a directory: %s", serverCfg.Model)
		}
	}
	if m.classifierController == nil {
		m.classifierController = llama.NewServerController(serverCfg, client)
	}

	m.logMessage("route_classifier_health", "Route classifier auto-start requested at "+baseURL+".")
	startCtx := ctx
	cancel := func() {}
	if seconds := m.cfg.RouteClassifier.StartTimeoutSeconds; seconds > 0 {
		startCtx, cancel = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	}
	defer cancel()

	if err := m.classifierController.Start(startCtx); err != nil {
		return fmt.Errorf("auto-start route classifier: %w", err)
	}
	m.managedRouteClassifier = true
	if err := m.classifierController.WaitUntilReady(startCtx); err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = m.classifierController.Stop(stopCtx)
		m.managedRouteClassifier = false
		return fmt.Errorf("wait for route classifier: %w", err)
	}
	m.logMessage("route_classifier_health", "Route classifier auto-started at "+baseURL+".")
	return nil
}

func validateRouteClassifierBinary(binary string) error {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return fmt.Errorf("route classifier binary is not configured")
	}
	if strings.ContainsAny(binary, `/\`) {
		if info, err := os.Stat(binary); err != nil {
			return fmt.Errorf("route classifier binary is not readable at %s: %w", binary, err)
		} else if info.IsDir() {
			return fmt.Errorf("route classifier binary path is a directory: %s", binary)
		}
		return nil
	}
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("route classifier binary %q was not found in PATH: %w", binary, err)
	}
	return nil
}

func routeClassifierHealthy(ctx context.Context, client *llama.Client) bool {
	if client == nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	models, err := client.ListModels(probeCtx)
	return err == nil && len(models) > 0
}
