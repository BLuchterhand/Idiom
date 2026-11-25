package comprehension

import (
	"path"
	"regexp"
	"strings"
)

// Files the localization config adds to the index beyond source code: data
// files (content_globs), shaders (shader_globs) and extra script extensions
// (extra_extensions). They are indexed as file nodes by the generic collector,
// so their text and path reach lexical retrieval; they carry no symbols.

const (
	languageContent = "content"
	languageShader  = "shader"
)

type extraSourceMatcher struct {
	content    []*regexp.Regexp
	shader     []*regexp.Regexp
	extensions map[string]string // ".ps1" -> language
}

func newExtraSourceMatcher(cfg LocalizationConfig) extraSourceMatcher {
	m := extraSourceMatcher{extensions: map[string]string{}}

	for _, glob := range cfg.ContentGlobs {
		m.content = appendGlob(m.content, glob)
	}

	for _, glob := range cfg.ShaderGlobs {
		m.shader = appendGlob(m.shader, glob)
	}

	for _, ext := range cfg.ExtraExtensions {
		ext = "." + strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
		m.extensions[ext] = extensionLanguage(ext)
	}

	return m
}

func appendGlob(into []*regexp.Regexp, glob string) []*regexp.Regexp {
	if re, err := regexp.Compile(globRegexp(glob)); err == nil {
		return append(into, re)
	}

	return into
}

// language returns the label of a file the config adds, or "" when it adds
// nothing for rel.
func (m extraSourceMatcher) language(rel string) string {
	for _, re := range m.content {
		if re.MatchString(rel) {
			return languageContent
		}
	}

	for _, re := range m.shader {
		if re.MatchString(rel) {
			return languageShader
		}
	}

	return m.extensions[strings.ToLower(path.Ext(rel))]
}

func extensionLanguage(ext string) string {
	switch ext {
	case ".ps1", ".psm1":
		return "powershell"
	case ".bat", ".cmd":
		return "batch"
	default:
		return strings.TrimPrefix(ext, ".")
	}
}

// globRegexp turns a slash glob into an anchored regexp: "**/" spans any
// directories (including none), "*" and "?" stay inside one path segment.
func globRegexp(glob string) string {
	var b strings.Builder

	b.WriteString("^")

	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")

			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")

			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}

	b.WriteString("$")

	return b.String()
}
