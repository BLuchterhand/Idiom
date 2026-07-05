package workspace

import (
	"strings"

	"idiom/internal/capabilities/workspace/patching"
)

// WritePreimagePaths returns workspace-relative paths whose pre-write
// contents should be snapshotted before dispatching req. The second return
// value is true when the request is a workspace write tool with a known
// preimage classification. Some write tools, such as mkdir, intentionally have
// no file preimage.
func WritePreimagePaths(req ToolRequest) ([]string, bool, error) {
	switch strings.TrimSpace(req.Tool) {
	case ToolNameEditFile, ToolNameWriteFile, ToolNameAppendFile, ToolNameDelegateEdit:
		if strings.TrimSpace(req.Path) == "" {
			return nil, true, nil
		}

		return []string{req.Path}, true, nil
	case ToolNameApplyPatch:
		ops, err := patching.Parse(req.Content)
		if err != nil {
			return nil, true, err
		}

		return patching.OpPaths(ops), true, nil
	case ToolNameGoMoveDeclarations:
		var paths []string

		if strings.TrimSpace(req.Path) != "" {
			paths = appendUniqueWorkspaceStrings(paths, req.Path)
		}

		if strings.TrimSpace(req.Destination) != "" {
			paths = appendUniqueWorkspaceStrings(paths, req.Destination)
		}

		for _, move := range req.Moves {
			if strings.TrimSpace(move.Destination) != "" {
				paths = appendUniqueWorkspaceStrings(paths, move.Destination)
			}
		}

		return paths, true, nil
	case ToolNameMkdir:
		return nil, true, nil
	default:
		return nil, false, nil
	}
}

func appendUniqueWorkspaceStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))

	var out []string

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	return out
}
