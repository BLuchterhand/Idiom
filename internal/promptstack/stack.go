package promptstack

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/config"
)

var autoProjectPromptCandidates = []string{
	".qwen.md",
	"QWEN.md",
	"IDIOM.md",
	"AGENTS.md",
	"CLAUDE.md",
	".idiom/discovery/context.md",
}

var userGlobalPromptCandidates = []string{
	"IDIOM.md",
	"AGENTS.md",
	"CLAUDE.md",
}

// userConfigDir returns the directory scanned for user-global prompts.
// Defaults to ~/.config/idiom; overridable via IDIOM_CONFIG_DIR for
// tests and unusual installs. Returns "" when the home dir is unknown
// and no override is set, in which case the user-global tier is skipped.
func userConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("IDIOM_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "idiom")
}

type Source struct {
	Kind        string
	DisplayName string
	Path        string
}

type Stack struct {
	Text    string
	Sources []Source
}

func Build(chat config.Chat, profiles []string) (Stack, error) {
	builder := stackBuilder{
		seen: make(map[string]struct{}),
	}

	if prompt := strings.TrimSpace(chat.SystemPrompt); prompt != "" {
		builder.addInline("inline", "inline", prompt)
	}

	for _, path := range chat.SystemPromptFiles {
		if err := builder.addFile("baseline", filepath.Base(path), path, true); err != nil {
			return Stack{}, err
		}
	}

	for _, profile := range profiles {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			continue
		}
		profilePath := filepath.Join(chat.ProfilesDir, profile+".md")
		if err := builder.addFile("profile", "profile:"+profile, profilePath, true); err != nil {
			return Stack{}, err
		}
	}

	if dir := userConfigDir(); dir != "" {
		for _, name := range userGlobalPromptCandidates {
			path := filepath.Join(dir, name)
			if err := builder.addFile("user_global", name, path, false); err != nil {
				return Stack{}, err
			}
		}
	}

	if chat.ProjectRoot != "" {
		candidates := autoProjectPromptCandidates
		if chat.ProjectPromptCandidates != nil {
			candidates = chat.ProjectPromptCandidates
		}
		for _, name := range candidates {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			path := filepath.Join(chat.ProjectRoot, name)
			if err := builder.addFile("project", name, path, false); err != nil {
				return Stack{}, err
			}
		}
	}

	for _, path := range chat.ProjectPromptFiles {
		if err := builder.addFile("project", filepath.Base(path), path, true); err != nil {
			return Stack{}, err
		}
	}

	return Stack{
		Text:    strings.Join(builder.sections, "\n\n"),
		Sources: builder.sources,
	}, nil
}

func ListProfiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read profiles dir: %w", err)
	}

	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		profiles = append(profiles, strings.TrimSuffix(name, ".md"))
	}

	sort.Strings(profiles)
	return profiles, nil
}

type stackBuilder struct {
	seen     map[string]struct{}
	sources  []Source
	sections []string
}

func (b *stackBuilder) addInline(kind, name, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	b.sources = append(b.sources, Source{
		Kind:        kind,
		DisplayName: name,
		Path:        "",
	})
	b.sections = append(b.sections, renderSection(kind, name, body))
}

func (b *stackBuilder) addFile(kind, name, path string, required bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		if required {
			return fmt.Errorf("%s prompt path is empty", kind)
		}
		return nil
	}
	path = filepath.Clean(path)
	if _, seen := b.seen[path]; seen {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !required && os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("load %s prompt %q: %w", kind, path, err)
	}

	body := strings.TrimSpace(string(data))
	if body == "" {
		if required {
			return fmt.Errorf("%s prompt %q is empty", kind, path)
		}
		return nil
	}

	b.seen[path] = struct{}{}
	b.sources = append(b.sources, Source{
		Kind:        kind,
		DisplayName: name,
		Path:        path,
	})
	b.sections = append(b.sections, renderSection(kind, name, body))
	return nil
}

func renderSection(kind, name, body string) string {
	label := kind
	switch kind {
	case "baseline":
		label = "baseline behavior"
	case "profile":
		label = "behavior profile"
	case "project":
		label = "project behavior"
	case "user_global":
		label = "user behavior"
	case "inline":
		label = "inline behavior"
	}
	return fmt.Sprintf("[%s: %s]\n%s", label, name, body)
}
