package writerrouting

import (
	"path/filepath"
	"strings"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

type ModelSelection struct {
	Config                    config.Config
	Aliases                   map[string]string
	PlannerModel              string
	ExecutorModel             string
	ClassifierModel           string
	UseBaseModelForClassifier bool
	UseDisplayModel           bool
}

func RoleAliases(projectRoot string, profileName string) map[string]string {
	file, ok, err := backends.LoadFromProjectRoot(projectRoot)
	if err != nil || !ok {
		return nil
	}

	profile, found := file.Profile(profileName)
	if !found {
		return nil
	}

	aliases := make(map[string]string, 3)
	for _, role := range []string{backends.RolePlanner, backends.RoleExecutor, backends.RoleClassifier} {
		if resolution := profile.ResolveRoleAlias(role); resolution.OK && strings.TrimSpace(resolution.Alias) != "" {
			aliases[role] = strings.TrimSpace(resolution.Alias)
		}
	}

	if len(aliases) == 0 {
		return nil
	}

	return aliases
}

func ModelForRole(selection ModelSelection, role string) string {
	switch role {
	case backends.RoleExecutor:
		if model := strings.TrimSpace(selection.ExecutorModel); model != "" {
			return model
		}
		if selection.Config.Implementer != nil && strings.TrimSpace(selection.Config.Implementer.Model) != "" {
			return displayModel(*selection.Config.Implementer, selection.UseDisplayModel)
		}
		if model := baseModel(selection); model != "" {
			return model
		}
	case backends.RoleClassifier:
		if model := strings.TrimSpace(selection.ClassifierModel); model != "" {
			return model
		}
		if model := strings.TrimSpace(selection.Config.RouteClassifier.Model); model != "" {
			return model
		}
		if selection.UseBaseModelForClassifier {
			if model := baseModel(selection); model != "" {
				return model
			}
		}
	default:
		if model := strings.TrimSpace(selection.PlannerModel); model != "" {
			return model
		}
		if model := baseModel(selection); model != "" {
			return model
		}
	}

	return strings.TrimSpace(selection.Aliases[role])
}

func baseModel(selection ModelSelection) string {
	return displayModel(selection.Config.LlamaServer, selection.UseDisplayModel)
}

func displayModel(server config.LlamaServer, useDisplay bool) string {
	if useDisplay {
		if display := strings.TrimSpace(server.DisplayName); display != "" {
			return display
		}
	}

	model := strings.TrimSpace(server.Model)
	if model == "" {
		return ""
	}

	if filepath.IsAbs(model) {
		if base := strings.TrimSpace(filepath.Base(model)); base != "" {
			return base
		}
	}

	return model
}
