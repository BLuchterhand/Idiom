package app

import (
	"fmt"
	"strings"
	"time"

	"idiom/internal/backends"
	"idiom/internal/config"
	"idiom/internal/llama"
)

func (m *Model) applyModelSwap(name string) {
	if m == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		m.status = "Usage: /model [list|name]"
		return
	}

	m.modelID = name
	m.modelDisplayID = name
	m.modelSwapped = true
	m.modelThinkingFloor = false
	m.maxTokensOverride = 0

	root := ""
	if m.workspace != nil {
		root = m.workspace.Root()
	}
	file, found, err := backends.LoadFromProjectRoot(root)
	if err != nil || !found {
		m.status = fmt.Sprintf("Model set to %s", name)
		return
	}
	model, ok := file.ModelByAlias(name)
	if !ok {
		m.status = fmt.Sprintf("Model set to %s", name)
		return
	}

	spec := backendsModelToLlamaServer(model, m.cfg.LlamaServer)
	m.plannerSpec = spec
	m.cfg.LlamaServer = spec
	m.modelThinkingFloor = spec.IsHosted()
	m.maxTokensOverride = model.MaxTokens
	m.modelID = resolveModelID(spec, spec.Model)
	m.modelDisplayID = modelSwapDisplayID(model, spec, name)

	timeout := time.Duration(m.cfg.Chat.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 900 * time.Second
	}
	client := llama.NewClientForServer(spec, timeout)
	m.client = client
	if spec.IsLaunchable() {
		m.controller = llama.NewServerController(spec, client)
	} else {
		m.controller = nil
	}
	m.status = fmt.Sprintf("Model set to %s", m.activeModelDisplayID())
}

func (m *Model) slashModel(_ string, args []string) bool {
	if m == nil {
		return false
	}
	if len(args) <= 1 {
		m.openModelPopup()
		return true
	}
	switch strings.TrimSpace(args[1]) {
	case "list":
		aliases := m.collectAllAliases()
		if len(aliases) == 0 {
			m.status = "No models discovered. Check .idiom/backends.json or server status."
			return true
		}
		m.status = "Available models: " + strings.Join(aliases, ", ")
	default:
		m.applyModelSwap(args[1])
	}
	return true
}

func modelSwapDisplayID(model backends.Model, spec config.LlamaServer, fallback string) string {
	s := strings.TrimSpace(model.Alias)
	if s == "" {
		return fallback
	}
	return s
}
