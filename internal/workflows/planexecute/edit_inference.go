package planexecute

import (
	"os"
	"path/filepath"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
)

func planExecuteAllPathsMissing(workspaceRoot string, paths []string) bool {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" || len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		path = plancontract.NormalizeScopePath(path)
		if path == "" {
			return false
		}

		if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(path))); err == nil {
			return false
		} else if !os.IsNotExist(err) {
			return false
		}
	}

	return true
}

func planExecuteImplicitBodyLooksSafeForPath(path string, body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return strings.HasPrefix(body, "package ")
	default:
		return true
	}
}

func planExecuteInferPathsForOldText(workspaceRoot string, required []string, oldText string) []string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	oldText = strings.TrimSpace(oldText)

	if workspaceRoot == "" || oldText == "" {
		return nil
	}

	var matches []string

	for _, requiredPath := range required {
		path := plancontract.NormalizeScopePath(requiredPath)
		if path == "" {
			continue
		}

		body, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(path)))
		if err != nil {
			continue
		}

		if strings.Count(string(body), oldText) == 1 {
			matches = append(matches, path)
		}
	}

	if len(matches) != 1 {
		return nil
	}

	return matches
}

func planExecuteExactEditBlockKind(label string) string {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(label)), ":. ")

	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")

	switch {
	case strings.HasPrefix(normalized, "old_text"), strings.HasPrefix(normalized, "old"):
		return "old"
	case strings.HasPrefix(normalized, "new_text"), strings.HasPrefix(normalized, "new"):
		return "new"
	case strings.HasPrefix(normalized, "replace_all"):
		return "replace_all"
	default:
		return ""
	}
}

func planExecuteMissingEditPaths(required []string, edits []planartifact.PlanEdit) []string {
	covered := make(map[string]struct{}, len(edits))
	for _, edit := range edits {
		path := strings.TrimSpace(edit.Path)
		if path != "" {
			covered[path] = struct{}{}
		}
	}

	var missing []string

	for _, path := range required {
		if _, ok := covered[path]; !ok {
			missing = append(missing, path)
		}
	}

	return missing
}
