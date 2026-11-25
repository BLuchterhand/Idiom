package comprehension

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

func TestGlobRegexp(t *testing.T) {
	for _, c := range []struct {
		glob, path string
		want       bool
	}{
		{"content/**/*.json", "content/a.json", true},
		{"content/**/*.json", "content/x/y/a.json", true},
		{"content/**/*.json", "other/a.json", false},
		{"content/**/*.json", "content/a.json.bak", false},
		{"game/*.gdshader", "game/sky.gdshader", true},
		{"game/*.gdshader", "game/shaders/sky.gdshader", false},
		{"**/*.json", "a.json", true},
	} {
		if got := regexp.MustCompile(globRegexp(c.glob)).MatchString(c.path); got != c.want {
			t.Errorf("glob %q on %q = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
}

func TestExtraSourceMatcherLanguages(t *testing.T) {
	m := newExtraSourceMatcher(LocalizationConfig{
		ContentGlobs: []string{"content/**/*.json"}, ShaderGlobs: []string{"game/**/*.gdshader"}, ExtraExtensions: []string{".ps1"},
	})

	for path, want := range map[string]string{
		"content/a/b.json":          languageContent,
		"game/shaders/sky.gdshader": languageShader,
		"scripts/run.PS1":           "powershell",
		"package.json":              "",
		"src/a.go":                  "",
	} {
		if got := m.language(path); got != want {
			t.Errorf("language(%q) = %q, want %q", path, got, want)
		}
	}
}

// With a localization section, data, shader and extra-extension files become
// file nodes and reach the lexical index; without one the index is unchanged.
func TestBuildIndexesConfiguredExtraFiles(t *testing.T) {
	files := map[string]string{
		"go.mod":                    "module example.com/extra\n\ngo 1.22\n",
		"main.go":                   "package main\n\nfunc main() {}\n",
		"content/species/fox.json":  `{"id": "red_fox", "habitat": "boreal"}`,
		"game/shaders/sky.gdshader": "shader_type sky; // twilight gradient\n",
		"scripts/pin.ps1":           "Write-Host pin_ecores\n",
		"other/ignored.json":        `{"id": "ignored"}`,
	}

	indexed := func(config string) []string {
		root := t.TempDir()

		for p, c := range files {
			full := filepath.Join(root, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		if config != "" {
			if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(root, ".idiom", "config.json"), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "auto"}); err != nil {
			t.Fatal(err)
		}

		graph, err := LoadGraph(root)
		if err != nil {
			t.Fatal(err)
		}

		var paths []string

		for _, n := range graph.Nodes {
			if n.Kind == "file" {
				paths = append(paths, n.Path)
			}
		}

		slices.Sort(paths)

		// The lexical index must carry the same files.
		corpus, err := LoadLexicalCorpus(root, []string{"red", "fox"})
		if config != "" && err != nil {
			t.Fatal(err)
		}

		if config != "" && len(corpus.Postings["fox"]) == 0 {
			t.Fatalf("content file missing from the lexical index: %v", corpus.Postings)
		}

		return paths
	}

	if got, want := indexed(""), []string{"main.go"}; !slices.Equal(got, want) {
		t.Fatalf("default index = %v, want %v", got, want)
	}

	cfg := `{"localization":{"content_globs":["content/**/*.json"],"shader_globs":["game/**/*.gdshader"],"extra_extensions":[".ps1"]}}`
	want := []string{"content/species/fox.json", "game/shaders/sky.gdshader", "main.go", "scripts/pin.ps1"}

	if got := indexed(cfg); !slices.Equal(got, want) {
		t.Fatalf("configured index = %v, want %v", got, want)
	}
}
