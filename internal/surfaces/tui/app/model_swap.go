package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
	"idiom/internal/runtime/llama"
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
	// MLX backends running thinking-capable models (e.g. Qwen 3.6) need
	// the thinking floor to prevent prompt_routing from disabling thinking
	// for read-only contracts like architecture_assessment.
	m.modelThinkingFloor = spec.IsHosted() || spec.IsMLX()
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
		aliases := m.collectAllAliases()
		if len(aliases) == 0 {
			m.status = "No models discovered. Check .idiom/backends.json or server status."
			return true
		}

		m.status = "Available models: " + strings.Join(aliases, ", ")
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

func (m *Model) collectAllAliases() []string {
	seen := make(map[string]bool)

	var names []string

	if file, found, err := backends.LoadFromProjectRoot(m.workspace.Root()); err == nil && found {
		if prof, ok := file.Profile(""); ok {
			for _, model := range prof.Models {
				if alias := strings.TrimSpace(model.Alias); alias != "" && !seen[alias] {
					seen[alias] = true
					names = append(names, alias)
				}
			}
		}

		for _, prof := range file.Profiles {
			for _, model := range prof.Models {
				if alias := strings.TrimSpace(model.Alias); alias != "" && !seen[alias] {
					seen[alias] = true
					names = append(names, alias)
				}
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if models, err := m.client.ListModels(ctx); err == nil {
		for _, mi := range models {
			id := strings.TrimSpace(mi.ID)
			if id != "" && !seen[id] {
				seen[id] = true
				names = append(names, id)
			}
		}
	}

	return names
}

func modelSwapDisplayID(model backends.Model, spec config.LlamaServer, fallback string) string {
	s := strings.TrimSpace(model.Alias)
	if s == "" {
		return fallback
	}

	return s
}
