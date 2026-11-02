package workspace

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"

	"idiom/internal/capabilities/workspace/externaltool"
	"idiom/internal/platform/config"
	"idiom/internal/platform/safepath"
)

// LoadExternalToolsRegistry merges inline workspace config with per-tool JSON
// files and returns a populated external tool registry. It returns nil when no
// external tools are configured.
func LoadExternalToolsRegistry(ws config.Workspace) (*externaltool.Registry, error) {
	tools := make([]externaltool.Tool, 0, len(ws.ExternalTools))
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

	slices.SortFunc(tools, func(a, b externaltool.Tool) int { return cmp.Compare(a.Name, b.Name) })

	return externaltool.NewRegistry(tools)
}

func externalToolFromConfig(def config.ExternalToolDef) externaltool.Tool {
	return externaltool.Tool{
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

func loadExternalToolsDir(dir string) ([]externaltool.Tool, error) {
	root, err := safepath.OpenRootPath(dir, "external tools directory")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("external_tools_dir %q: %w", dir, err)
	}
	defer root.Close()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("external_tools_dir %q: %w", dir, err)
	}

	out := make([]externaltool.Tool, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		file, err := safepath.OpenFileFromRoot(root, dir, entry.Name(), "external tool file", safepath.Options{})
		if err != nil {
			return nil, fmt.Errorf("external tool file %q: %w", path, err)
		}
		raw, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("external tool file %q: %w", path, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("external tool file %q: %w", path, closeErr)
		}

		var def config.ExternalToolDef
		if err := json.Unmarshal(raw, &def); err != nil {
			return nil, fmt.Errorf("external tool file %q: parse: %w", path, err)
		}

		out = append(out, externalToolFromConfig(def))
	}

	return out, nil
}
