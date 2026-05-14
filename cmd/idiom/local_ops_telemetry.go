package main

import (
	"strings"

	"idiom/internal/config"
)

func localOpsTelemetryFields(cfg config.Config, lane string) map[string]any {
	ctxSize := cfg.LlamaServer.CtxSize
	maxOutput := cfg.Chat.MaxTokens
	contextState := "pass"
	switch {
	case ctxSize <= 0:
		contextState = "unavailable"
	case maxOutput <= 0:
		contextState = "degraded"
	case maxOutput >= ctxSize:
		contextState = "degraded"
	}

	classifierState := "disabled_fallback"
	if cfg.RouteClassifier.Enabled {
		classifierState = "configured"
		if strings.TrimSpace(cfg.RouteClassifier.BaseURL) == "" && cfg.RouteClassifier.Port <= 0 {
			classifierState = "degraded"
		}
	}

	return map[string]any{
		"lane":                         strings.TrimSpace(lane),
		"backend_kind":                 firstNonEmptyCLIString(cfg.LlamaServer.Kind, "llama"),
		"backend_configured":           strings.TrimSpace(cfg.LlamaServer.Model) != "",
		"backend_port":                 cfg.LlamaServer.Port,
		"backend_ctx_size":             ctxSize,
		"context_budget_state":         contextState,
		"context_budget_ctx_size":      ctxSize,
		"context_budget_max_output":    maxOutput,
		"classifier_enabled":           cfg.RouteClassifier.Enabled,
		"classifier_state":             classifierState,
		"classifier_timeout_seconds":   cfg.RouteClassifier.TimeoutSeconds,
		"classifier_autostart_enabled": cfg.RouteClassifier.AutoStartServer,
		"degraded":                     contextState != "pass" || classifierState != "configured",
	}
}
