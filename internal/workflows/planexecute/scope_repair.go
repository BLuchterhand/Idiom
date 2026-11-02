package planexecute

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/safepath"
)

type candidatePathRepair struct {
	Original string `json:"original"`
	Resolved string `json:"resolved"`
	Reason   string `json:"reason"`
}

func repairMissingAllowedCandidatePaths(root string, paths []string) ([]string, []candidatePathRepair) {
	if strings.TrimSpace(root) == "" || len(paths) == 0 {
		return paths, nil
	}

	var (
		repaired []string
		repairs  []candidatePathRepair
	)

	for _, path := range paths {
		if pathExistsUnderRoot(root, path) {
			repaired = append(repaired, path)
			continue
		}

		resolved, ok := uniqueCodeFileByBase(root, filepath.Base(path))
		if !ok {
			repaired = append(repaired, path)
			continue
		}

		repaired = append(repaired, resolved)
		repairs = append(repairs, candidatePathRepair{
			Original: path,
			Resolved: resolved,
			Reason:   "missing_candidate_unique_basename_match",
		})
	}

	return mergePlanExecutePaths(repaired), repairs
}

func applyCandidatePathRepairsToPrompt(prompt string, repairs []candidatePathRepair) string {
	if len(repairs) == 0 || strings.TrimSpace(prompt) == "" {
		return prompt
	}

	repaired := prompt
	var notes []string
	for _, repair := range repairs {
		original := strings.TrimSpace(repair.Original)
		resolved := strings.TrimSpace(repair.Resolved)
		if original == "" || resolved == "" || original == resolved {
			continue
		}

		repaired = strings.ReplaceAll(repaired, original, resolved)
		notes = append(notes, original+" -> "+resolved)
	}

	if len(notes) > 0 {
		repaired = strings.TrimSpace(repaired) + "\n\nPath repair metadata: original user references resolved by the harness; use the resolved paths as canonical source/read/edit targets: " + strings.Join(notes, "; ")
	}

	return repaired
}

func pathExistsUnderRoot(root string, path string) bool {
	full, _, err := safepath.UnderRoot(root, path, "candidate path", safepath.Options{})
	if err != nil {
		return false
	}

	info, err := os.Stat(full)
	return err == nil && !info.IsDir()
}

func uniqueCodeFileByBase(root string, base string) (string, bool) {
	base = strings.TrimSpace(base)
	if base == "" || strings.Contains(base, string(filepath.Separator)) {
		return "", false
	}

	var matches []string
	err := safepath.WalkDirUnderRoot(root, root, "plan candidate path discovery", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".idiom", "node_modules", "vendor", "dist", "build", "target":
				if path != root {
					return filepath.SkipDir
				}
			}

			return nil
		}

		if entry.Name() != base || !planExecuteCandidateCodeFile(path) {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			return nil
		}

		matches = append(matches, filepath.ToSlash(rel))
		return nil
	})
	if err != nil || len(matches) != 1 {
		return "", false
	}

	return matches[0], true
}

func planExecuteCandidateCodeFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".gd":
		return true
	default:
		return false
	}
}
