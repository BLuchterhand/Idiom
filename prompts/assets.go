package prompts

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

//go:embed base.md
var basePrompt []byte

//go:embed profiles/*.md
var profilesFS embed.FS

func BasePrompt() string {
	return string(basePrompt)
}

func Profile(name string) (string, error) {
	body, err := profilesFS.ReadFile("profiles/" + name)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func MustProfile(name string) string {
	body, err := Profile(name)
	if err != nil {
		panic(fmt.Sprintf("missing embedded profile %s: %v", name, err))
	}

	return body
}

func ProfileNames() ([]string, error) {
	entries, err := profilesFS.ReadDir("profiles")
	if err != nil {
		return nil, fmt.Errorf("read embedded profiles/: %w", err)
	}

	var names []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.TrimSuffix(name, ".md") == name {
			continue
		}

		if _, err := fs.Stat(profilesFS, "profiles/"+name); err != nil {
			continue
		}

		names = append(names, strings.TrimSuffix(name, ".md"))
	}

	slices.Sort(names)

	return names, nil
}
