package llmrerank

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxSymbolsPerFile = 5

// Doc is one candidate file as the scorers see it.
type Doc struct {
	Path string
	Text string
}

type codeMapEntry struct {
	purpose string
	symbols []string
}

var codeMapLine = regexp.MustCompile(`^- (\S+) \(\d+\)(?: (.*))?$`)

// LoadDocs builds one Doc per path from the repository's code map: the path,
// the file's one-line purpose and its first public symbols. A missing code map
// leaves the bare path, so ranking still works without it.
func LoadDocs(root, codeMapPath string, paths []string) []Doc {
	entries := parseCodeMap(filepath.Join(root, filepath.FromSlash(codeMapPath)))
	docs := make([]Doc, len(paths))

	for i, p := range paths {
		text := p

		if e, ok := entries[p]; ok {
			if e.purpose != "" {
				text += " - " + e.purpose
			}

			if len(e.symbols) > 0 {
				text += " [" + strings.Join(e.symbols, ", ") + "]"
			}
		}

		docs[i] = Doc{Path: p, Text: text}
	}

	return docs
}

func parseCodeMap(path string) map[string]codeMapEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	entries := map[string]codeMapEntry{}
	dir := ""

	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			dir = strings.TrimSpace(rest)
			continue
		}

		m := codeMapLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}

		full := m[1]
		if dir != "" && dir != "." {
			full = dir + "/" + m[1]
		}

		entries[full] = splitCodeMapRest(m[2])
	}

	return entries
}

// splitCodeMapRest splits "purpose | :15 class A, :21 fn b" into the purpose
// and the symbol names.
func splitCodeMapRest(rest string) codeMapEntry {
	purpose, decls, hasDecls := strings.Cut(rest, " | ")
	if strings.HasPrefix(rest, "| ") {
		purpose, decls, hasDecls = "", rest[2:], true
	}

	e := codeMapEntry{purpose: strings.TrimSpace(purpose)}
	if !hasDecls {
		return e
	}

	for _, d := range strings.Split(decls, ", ") {
		fields := strings.Fields(d)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], ":") {
			continue
		}

		e.symbols = append(e.symbols, fields[len(fields)-1])
		if len(e.symbols) == maxSymbolsPerFile {
			break
		}
	}

	return e
}
