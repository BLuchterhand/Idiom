package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
)

// setupExternalToolsRegistry merges Workspace.ExternalTools (inline) with
// any per-tool JSON files under Workspace.ExternalToolsDir, then returns
// a populated registry. Returns (nil, nil) when no tools are defined
// from either source. Errors stop registration so the user sees a clear
// startup message instead of silently missing tools.
//
// Generic by construction: the registry holds whatever tools the host
// project lists. Idiom does not bundle, default, or special-case any
// project's tool catalog.
func setupExternalToolsRegistry(ws config.Workspace) (*workspace.ExternalToolRegistry, error) {
	tools := make([]workspace.ExternalTool, 0, len(ws.ExternalTools))
	for _, def := range ws.ExternalTools {
		tools = append(tools, externalToolFromConfig(def))
	}

	if dir := ws.ExternalToolsDir; dir != "" {
		dirPath := dir
		if !filepath.IsAbs(dirPath) && ws.Root != "" {
			dirPath = filepath.Join(ws.Root, dirPath)
		}

		dirTools, err := loadExternalToolsDir(dirPath)
		if err != nil {
			return nil, err
		}

		tools = append(tools, dirTools...)
	}

	if len(tools) == 0 {
		return nil, nil
	}

	// Stable order makes startup logging + tool-spec ordering deterministic.
	slices.SortFunc(tools, func(a, b workspace.ExternalTool) int { return cmp.Compare(a.Name, b.Name) })

	return workspace.NewExternalToolRegistry(tools)
}

func externalToolFromConfig(def config.ExternalToolDef) workspace.ExternalTool {
	return workspace.ExternalTool{
		Name:           def.Name,
		Description:    def.Description,
		Command:        def.Command,
		Args:           append([]string(nil), def.Args...),
		Cwd:            def.Cwd,
		TimeoutSeconds: def.TimeoutSeconds,
		InputSchema:    def.InputSchema,
		ReadOnly:       def.ReadOnly,
		Discovery:      def.Discovery,
	}
}

// loadExternalToolsDir reads every *.json file in dir and returns the
// parsed ExternalTool entries. Sub-directories are not recursed. Files
// failing to parse are reported with their path for easy diagnosis.
func loadExternalToolsDir(dir string) ([]workspace.ExternalTool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("external_tools_dir %q: %w", dir, err)
	}

	out := make([]workspace.ExternalTool, 0)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(dir, entry.Name())

		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("external tool file %q: %w", path, err)
		}

		var def config.ExternalToolDef

		if err := json.Unmarshal(raw, &def); err != nil {
			return nil, fmt.Errorf("external tool file %q: parse: %w", path, err)
		}

		out = append(out, externalToolFromConfig(def))
	}

	return out, nil
}
