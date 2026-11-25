package comprehension

import (
	"path"
	"regexp"
	"strings"

	"idiom/internal/platform/textutil"
)

// Role and reason labels for pack candidates. They are computed only when a
// pack is requested with Explain, so default pack output is unchanged. The
// role comes from the path shape and the edge that admitted the file; the
// reason is a one-line, deterministic sentence of at most PackWhyMaxChars.

var (
	packTestPathRE   = regexp.MustCompile(`(^|/)(tests?|__tests__)/|_test\.|(^|/)test_|\.test\.|\.spec\.`)
	packShaderExts   = map[string]bool{".gdshader": true, ".glsl": true, ".wgsl": true, ".hlsl": true, ".shader": true}
	packContentExts  = map[string]bool{".json": true, ".yaml": true, ".yml": true, ".toml": true, ".csv": true}
	packScriptExts   = map[string]bool{".sh": true, ".bash": true, ".ps1": true, ".mjs": true, ".cjs": true, ".bat": true}
	packDocExts      = map[string]bool{".md": true, ".rst": true, ".txt": true, ".adoc": true}
	packToolDirs     = map[string]bool{"tools": true, "cmd": true, "tooling": true}
	packUIDirSegment = map[string]bool{"ui": true, "hud": true, "menu": true, "menus": true}
)

const (
	edgeDirectionOut = "out" // the admitted file is the edge source
	edgeDirectionIn  = "in"  // the admitted file is the edge target
	packDir2Depth    = 2
	maxWhyTerms      = 3
)

// PathDir2 is the subtopic key of a file: its first two directory components
// (fewer when the file sits higher; "." at the root). The eval harness scores
// subtopic coverage with the same key the pack reports in PackFile.Dir2.
func PathDir2(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) <= 1 {
		return "."
	}

	dirs := parts[:len(parts)-1]
	if len(dirs) > packDir2Depth {
		dirs = dirs[:packDir2Depth]
	}

	return strings.Join(dirs, "/")
}

// explainPackFiles fills Kind, Dir2 and Why on every file.
func explainPackFiles(files []PackFile, terms []string) {
	for i := range files {
		files[i].Dir2 = PathDir2(files[i].Path)
		files[i].Kind = packFileKind(files[i])
		files[i].Why = packFileWhy(files[i], terms)
	}
}

func packFileKind(file PackFile) PackFileKind {
	p := strings.ToLower(file.Path)
	ext := path.Ext(p)

	switch {
	case file.Role == "test" || packTestPathRE.MatchString(p):
		return PackFileKindTest
	case packDocExts[ext]:
		return PackFileKindDoc
	case packShaderExts[ext]:
		return PackFileKindShader
	case packContentExts[ext]:
		return PackFileKindContent
	case packScriptExts[ext]:
		return PackFileKindScript
	}

	segments := strings.Split(p, "/")
	for _, seg := range segments[:len(segments)-1] {
		if packUIDirSegment[seg] {
			return PackFileKindUI
		}
	}

	if packToolDirs[segments[0]] {
		return PackFileKindTool
	}

	return packAdmittedKind(file)
}

func packAdmittedKind(file PackFile) PackFileKind {
	adm := file.AdmittedBy
	if adm == nil || file.Role == "editable" {
		return PackFileKindDef
	}

	switch {
	case adm.Direction == edgeDirectionIn:
		return PackFileKindCallee
	case adm.EdgeKind == "calls":
		return PackFileKindCaller
	default:
		return PackFileKindConsumer
	}
}

var edgeVerbs = map[string][2]string{ // kind -> {admitted is source, admitted is target}
	"calls":      {"calls", "called by"},
	"imports":    {"imports", "imported by"},
	"tests":      {"tests", "tested by"},
	"references": {"references", "referenced by"},
	"defines":    {"defines", "defined in"},
	"contains":   {"contains", "in"},
}

func packFileWhy(file PackFile, terms []string) string {
	adm := file.AdmittedBy
	if adm != nil && file.Role != "editable" {
		verbs, ok := edgeVerbs[adm.EdgeKind]
		verb := adm.EdgeKind
		if ok && adm.Direction == edgeDirectionIn {
			verb = verbs[1]
		} else if ok {
			verb = verbs[0]
		}

		return truncateWhy(verb + " " + adm.Object)
	}

	if matched := matchingPathTerms(file.Path, terms); len(matched) > 0 {
		return truncateWhy("matches \"" + strings.Join(matched, "\",\"") + "\" in path")
	}

	return "defines symbols matching task terms"
}

func matchingPathTerms(p string, terms []string) []string {
	lower := strings.ToLower(p)

	var out []string

	seen := map[string]struct{}{}

	for _, term := range terms {
		t := strings.TrimSpace(strings.ToLower(term))
		if _, dup := seen[t]; len(t) < 3 || dup || !strings.Contains(lower, t) {
			continue
		}

		seen[t] = struct{}{}

		out = append(out, t)
		if len(out) == maxWhyTerms {
			break
		}
	}

	return out
}

func truncateWhy(s string) string {
	runes := []rune(s)
	if len(runes) <= PackWhyMaxChars {
		return s
	}

	return string(runes[:PackWhyMaxChars-1]) + "…"
}

// admitIfNew records how a file entered the pack through a relationship edge.
// Only a file that was not already a candidate gets an admission, so direct
// matches keep none.
func (b *packFileBuilder) admitIfNew(p string, wasPresent bool, adm PackAdmission) {
	if wasPresent {
		return
	}

	file, ok := b.files[p]
	if !ok || file.AdmittedBy != nil {
		return
	}

	file.AdmittedBy = &adm
	b.files[p] = file
}

func (b *packFileBuilder) has(p string) bool {
	_, ok := b.files[p]

	return ok
}

func admissionObject(n Node) string {
	return textutil.FirstNonEmpty(n.QualifiedName, path.Base(n.Path))
}
