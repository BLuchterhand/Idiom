package cliassets

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// BriefPreflightScript is the Python brief-lint implementation bundled into
// the idiom binary.
//
//go:embed brief-preflight.py
var briefPreflightScript []byte

//go:embed brief-preload/*.md
var briefPreloadFS embed.FS

//go:embed lanes
var lanesFS embed.FS

//go:embed profiles/*.md
var profilesFS embed.FS

//go:embed skills/*/SKILL.md
var skillsFS embed.FS

var canonicalLaneRuleNames = []string{
	"loop-protocol.md",
	"brief-skeleton.md",
	"known-traps-generic.md",
}

func BriefPreflightScript() []byte {
	return append([]byte(nil), briefPreflightScript...)
}

func BriefPreload(kind string) (string, error) {
	body, err := briefPreloadFS.ReadFile("brief-preload/" + kind + ".md")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func BriefPreloadKinds() ([]string, error) {
	return markdownBasenames(briefPreloadFS, "brief-preload")
}

func CanonicalLaneRuleNames() []string {
	return append([]string(nil), canonicalLaneRuleNames...)
}

func LanePrompt(mode, filename string) (string, error) {
	body, err := lanesFS.ReadFile("lanes/" + mode + "/" + filename)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func LaneRule(name string) (string, error) {
	body, err := lanesFS.ReadFile("lanes/rules/" + name)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func MustRefineTemplate(name string) string {
	body, err := LanePrompt("refine", name)
	if err != nil {
		panic(fmt.Sprintf("missing embedded refine template %s: %v", name, err))
	}
	return body
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

func Skill(name string) ([]byte, error) {
	body, err := skillsFS.ReadFile("skills/" + name + "/SKILL.md")
	if err != nil {
		return nil, err
	}
	return body, nil
}

func SkillNames() ([]string, error) {
	entries, err := skillsFS.ReadDir("skills")
	if err != nil {
		return nil, fmt.Errorf("read embedded skills/: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := fs.Stat(skillsFS, "skills/"+e.Name()+"/SKILL.md"); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names, nil
}

func markdownBasenames(fsys fs.ReadDirFS, dir string) ([]string, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == e.Name() {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}
