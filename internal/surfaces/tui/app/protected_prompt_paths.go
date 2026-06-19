package app

import (
	"strings"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/modepolicy"
)

func protectedPromptWritePaths(cfg config.Config, mode modepolicy.Mode) []string {
	var paths []string

	paths = appendPromptProtectionPaths(paths, cfg.Chat.SystemPromptFiles...)
	paths = appendPromptProtectionPaths(paths, mode.SystemPromptFiles...)
	paths = appendPromptProtectionPaths(paths, cfg.Chat.ProjectPromptFiles...)
	paths = appendPromptProtectionPaths(paths,
		"prompts/base.md",
		".idiom/prompts/base.md",
	)

	return paths
}

func appendPromptProtectionPaths(paths []string, candidates ...string) []string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		paths = appendUniqueBriefStrings(paths, candidate)
	}

	return paths
}
