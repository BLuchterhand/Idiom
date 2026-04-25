package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/discovery"
)

// Fix 4 (workspace overview) — cheap one-level directory listing that gets
// injected into the opening system context so small models stop guessing at
// top-level paths. See docs/opencode-fixes-design.md §4.

const (
	workspaceOverviewMaxRootEntries  = 40
	workspaceOverviewMaxFilesPerDir  = 6
	workspaceOverviewMaxSubdirs      = 20
	workspaceOverviewMaxContentBytes = 2048
)

// OverviewSystemPrompt returns a short system-message-friendly description
// of the workspace layout at root, or the empty string when root is not a
// readable directory. The returned text is bounded and idempotent — callers
// may inject it once per session without worrying about bloat.
func OverviewSystemPrompt(root string) string {
	cleaned := strings.TrimSpace(root)
	if cleaned == "" {
		return ""
	}
	info, err := os.Stat(cleaned)
	if err != nil || !info.IsDir() {
		return ""
	}

	entries, err := os.ReadDir(cleaned)
	if err != nil {
		return ""
	}

	dirs := make([]os.DirEntry, 0, len(entries))
	topFiles := make([]string, 0, len(entries))
	notable := make([]string, 0, 8)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && discovery.ShouldSkipDirName(name) {
			continue
		}
		if !e.IsDir() && discovery.ShouldSkipRelFile(name) {
			continue
		}
		if strings.HasPrefix(name, ".") {
			if isNotableDotfile(name) {
				notable = append(notable, name)
			}
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
			continue
		}
		topFiles = append(topFiles, name)
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Strings(topFiles)
	sort.Strings(notable)

	var b strings.Builder
	b.WriteString("Workspace overview (top-level layout, cheap snapshot at session start):\n")
	b.WriteString("Root: ")
	b.WriteString(filepath.Clean(cleaned))
	b.WriteString("\n")

	if len(topFiles) > 0 {
		b.WriteString("Top-level files: ")
		b.WriteString(strings.Join(limitStringsOverview(topFiles, workspaceOverviewMaxRootEntries), ", "))
		b.WriteString("\n")
	}
	if len(notable) > 0 {
		b.WriteString("Notable dotfiles: ")
		b.WriteString(strings.Join(limitStringsOverview(notable, 10), ", "))
		b.WriteString("\n")
	}
	if len(dirs) > 0 {
		b.WriteString("\nTop-level directories:\n")
		for i, d := range dirs {
			if i >= workspaceOverviewMaxSubdirs {
				b.WriteString(fmt.Sprintf("  ... %d more directories omitted\n", len(dirs)-workspaceOverviewMaxSubdirs))
				break
			}
			sub := filepath.Join(cleaned, d.Name())
			count, sample := summarizeSubdir(sub, d.Name()+"/")
			fmt.Fprintf(&b, "  %s/ (%d files)", d.Name(), count)
			if len(sample) > 0 {
				fmt.Fprintf(&b, " — e.g. %s", strings.Join(sample, ", "))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nUse this only as a starting map; verify paths with search_files / list_files / read_file before editing.")
	out := b.String()
	if len(out) > workspaceOverviewMaxContentBytes {
		out = out[:workspaceOverviewMaxContentBytes] + "\n... (overview truncated)"
	}
	return out
}

func isNotableDotfile(name string) bool {
	switch name {
	case ".opencode", ".github", ".vscode", ".gitignore",
		".golangci.yml", ".golangci.yaml", ".editorconfig", ".env.example",
		".tool-versions", ".python-version", ".nvmrc":
		return true
	}
	return false
}

func summarizeSubdir(path string, prefix string) (int, []string) {
	count, sample := collectDirSample(path, prefix)
	if count == 0 {
		// Descend one more level — dirs that only contain nested packages
		// (the common Go layout like internal/app/…) should still surface a
		// useful sample.
		entries, err := os.ReadDir(path)
		if err != nil {
			return 0, nil
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || discovery.ShouldSkipDirName(name) {
				continue
			}
			sub := filepath.Join(path, e.Name())
			c, s := collectDirSample(sub, prefix+e.Name()+"/")
			count += c
			sample = append(sample, s...)
			if len(sample) >= workspaceOverviewMaxFilesPerDir {
				break
			}
		}
	}
	sort.Strings(sample)
	if len(sample) > workspaceOverviewMaxFilesPerDir {
		sample = sample[:workspaceOverviewMaxFilesPerDir]
	}
	return count, sample
}

func collectDirSample(path string, prefix string) (int, []string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, nil
	}
	count := 0
	sample := make([]string, 0, workspaceOverviewMaxFilesPerDir)
	for _, e := range entries {
		name := e.Name()
		rel := prefix + name
		if strings.HasPrefix(name, ".") {
			continue
		}
		if discovery.ShouldSkipRelFile(rel) {
			continue
		}
		if e.IsDir() {
			continue
		}
		count++
		if len(sample) < workspaceOverviewMaxFilesPerDir {
			sample = append(sample, prefix+name)
		}
	}
	return count, sample
}

func limitStringsOverview(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	out := make([]string, limit, limit+1)
	copy(out, values[:limit])
	return append(out, fmt.Sprintf("… (+%d more)", len(values)-limit))
}
