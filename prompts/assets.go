package prompts

import (
	"embed"
	"fmt"
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
