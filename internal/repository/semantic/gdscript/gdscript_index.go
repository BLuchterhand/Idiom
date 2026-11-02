package gdscript

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/platform/safepath"
	"idiom/internal/repository/discovery"
	"idiom/internal/repository/semantic/internal/indexsupport"
)

type Index struct {
	Root  string
	Files []File
}

type File struct {
	Path       string
	ClassName  string
	Symbols    []Symbol
	References []Reference
	Calls      []Call
	Resources  []ResourceReference
}

type Symbol struct {
	Name      string
	Qualified string
	Kind      string
	Path      string
	Line      int
	EndLine   int
	Static    bool
}

type Reference struct {
	Name       string
	Path       string
	Line       int
	Kind       string
	TargetPath string
}

type Call struct {
	Caller      Symbol
	Target      string
	Qualifier   string
	TargetClass string
	Line        int
	Kind        string
}

type CallsResult struct {
	Resolved             []Call
	UnresolvedCandidates []Call
}

type ResourceReference struct {
	Path string
	Line int
	Kind string
}

var (
	gdClassNameRE           = regexp.MustCompile(`^\s*class_name\s+([A-Za-z_][A-Za-z0-9_]*)`)
	gdFunctionRE            = regexp.MustCompile(`^\s*(?:(?:@[A-Za-z_][A-Za-z0-9_]*(?:\([^)]*\))?)\s+)*(static\s+)?func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	gdSignalRE              = regexp.MustCompile(`^\s*signal\s+([A-Za-z_][A-Za-z0-9_]*)`)
	gdCallRE                = regexp.MustCompile(`(?:\b([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	gdSignalMethodRE        = regexp.MustCompile(`(?:\b([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*(emit|connect)\s*\(`)
	gdIdentifierRE          = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
	gdResourceRE            = regexp.MustCompile(`\b(preload|load)\s*\(\s*["']([^"']+)["']\s*\)`)
	gdConnectRE             = regexp.MustCompile(`\.connect\s*\(\s*(?:Callable\s*\(\s*[^,]+,\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	gdCallableNameRE        = regexp.MustCompile(`Callable\s*\([^,]+,\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\)`)
	gdPreloadAliasRE        = regexp.MustCompile(`^\s*const\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*preload\s*\(\s*["']res://([^"']+)["']\s*\)`)
	gdTypedVariableRE       = regexp.MustCompile(`\b(?:var|const)\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*([A-Za-z_][A-Za-z0-9_]*)`)
	gdConstructedVariableRE = regexp.MustCompile(`\bvar\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*new\s*\(`)
)

// BuildIndex parses GDScript declarations and local relationship evidence
// without requiring a Godot executable.
func BuildIndex(root string) (*Index, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("GDScript workspace root is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve GDScript workspace root: %w", err)
	}

	paths := make([]string, 0, 32)
	err = safepath.WalkDirUnderRoot(absoluteRoot, absoluteRoot, "GDScript index", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != absoluteRoot && discovery.ShouldSkipDirName(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".gd") {
			return nil
		}
		rel, relErr := filepath.Rel(absoluteRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if discovery.ShouldSkipRelFile(rel) {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk GDScript workspace: %w", err)
	}
	slices.Sort(paths)

	index := &Index{Root: absoluteRoot, Files: make([]File, 0, len(paths))}
	classNames := make(map[string]string, len(paths))
	sources := make(map[string]string, len(paths))
	for _, rel := range paths {
		fullPath := filepath.Join(absoluteRoot, filepath.FromSlash(rel))
		body, readErr := indexsupport.ReadFileUnderRoot(absoluteRoot, fullPath)
		if readErr != nil {
			continue
		}
		file := parseFile(rel, string(body))
		sources[rel] = string(body)
		index.Files = append(index.Files, file)
		if file.ClassName != "" {
			classNames[rel] = file.ClassName
		}
	}

	for fileIndex := range index.Files {
		file := &index.Files[fileIndex]
		aliases := make(map[string]string)
		for _, line := range strings.Split(sources[file.Path], "\n") {
			if match := gdPreloadAliasRE.FindStringSubmatch(line); len(match) == 3 {
				aliases[match[1]] = classNames[resolveResourcePath("res://"+match[2])]
			}
		}
		for callIndex := range file.Calls {
			call := &file.Calls[callIndex]
			if call.Qualifier != "" {
				call.TargetClass = resolveReceiverClass(sources[file.Path], call.Line, call.Qualifier, classNames, aliases)
			} else {
				call.TargetClass = file.ClassName
			}
		}
	}

	for fileIndex := range index.Files {
		file := &index.Files[fileIndex]
		for _, resource := range file.Resources {
			target := resolveResourcePath(resource.Path)
			if target == "" {
				continue
			}
			file.References = append(file.References, Reference{
				Name:       filepath.Base(target),
				Path:       file.Path,
				Line:       resource.Line,
				Kind:       resource.Kind,
				TargetPath: target,
			})
			if className := classNames[target]; className != "" {
				file.References = append(file.References, Reference{
					Name:       className,
					Path:       file.Path,
					Line:       resource.Line,
					Kind:       resource.Kind,
					TargetPath: target,
				})
			}
		}
	}

	return index, nil
}

func resolveReceiverClass(source string, line int, receiver string, classes map[string]string, aliases map[string]string) string {
	if aliases[receiver] != "" {
		return aliases[receiver]
	}
	for _, className := range classes {
		if className == receiver {
			return receiver
		}
	}
	lines := strings.Split(source, "\n")
	if line > len(lines) {
		return ""
	}
	types := make(map[string]string)
	for _, sourceLine := range lines[:line] {
		if match := gdTypedVariableRE.FindStringSubmatch(sourceLine); len(match) == 3 {
			types[match[1]] = match[2]
		}
		if match := gdConstructedVariableRE.FindStringSubmatch(sourceLine); len(match) == 3 {
			className := match[2]
			if aliases[className] != "" {
				className = aliases[className]
			}
			types[match[1]] = className
		}
	}
	if types[receiver] != "" {
		return types[receiver]
	}
	return ""
}

func parseFile(path string, body string) File {
	lines := strings.Split(body, "\n")
	file := File{Path: path}
	for i, line := range lines {
		clean := stripComment(line)
		if match := gdClassNameRE.FindStringSubmatch(clean); len(match) == 2 {
			file.ClassName = match[1]
			file.Symbols = append(file.Symbols, Symbol{Name: match[1], Qualified: match[1], Kind: "class", Path: path, Line: i + 1, EndLine: i + 1})
		}
		if match := gdResourceRE.FindAllStringSubmatch(clean, -1); len(match) > 0 {
			for _, resource := range match {
				file.Resources = append(file.Resources, ResourceReference{Path: resource[2], Line: i + 1, Kind: resource[1]})
			}
		}
	}

	for i, line := range lines {
		clean := codeOnly(line)
		if match := gdFunctionRE.FindStringSubmatch(clean); len(match) == 3 {
			indent := indentationWidth(line)
			end := functionEndLine(lines, i, indent)
			kind := "method"
			qualified := match[2]
			if file.ClassName != "" {
				qualified = file.ClassName + "." + match[2]
			} else {
				kind = "function"
			}
			static := strings.TrimSpace(match[1]) != ""
			if static {
				kind = "static_method"
			}
			symbol := Symbol{Name: match[2], Qualified: qualified, Kind: kind, Path: path, Line: i + 1, EndLine: end, Static: static}
			file.Symbols = append(file.Symbols, symbol)
			file.Calls = append(file.Calls, parseCalls(lines, i, end, symbol)...)
			continue
		}
		if match := gdSignalRE.FindStringSubmatch(clean); len(match) == 2 {
			qualified := match[1]
			if file.ClassName != "" {
				qualified = file.ClassName + "." + match[1]
			}
			file.Symbols = append(file.Symbols, Symbol{Name: match[1], Qualified: qualified, Kind: "signal", Path: path, Line: i + 1, EndLine: i + 1})
		}
	}
	file.References = append(file.References, parseReferences(lines, path)...)

	return file
}

func parseCalls(lines []string, functionLine int, endLine int, caller Symbol) []Call {
	calls := make([]Call, 0, 8)
	for i := functionLine + 1; i < endLine; i++ {
		code := codeOnly(lines[i])
		for _, match := range gdSignalMethodRE.FindAllStringSubmatch(code, -1) {
			kind := "signal_emit"
			if match[3] == "connect" {
				kind = "signal_connect"
			}
			calls = append(calls, Call{Caller: caller, Target: match[2], Qualifier: match[1], Line: i + 1, Kind: kind})
		}
		for _, match := range gdCallRE.FindAllStringSubmatch(code, -1) {
			qualifier := ""
			if match[1] != "" {
				qualifier = match[1]
			}
			calls = append(calls, Call{Caller: caller, Target: match[2], Qualifier: qualifier, Line: i + 1, Kind: "call"})
		}
		for _, match := range gdConnectRE.FindAllStringSubmatch(code, -1) {
			calls = append(calls, Call{Caller: caller, Target: match[1], Line: i + 1, Kind: "signal_connect"})
		}
		for _, match := range gdCallableNameRE.FindAllStringSubmatch(stripComment(lines[i]), -1) {
			calls = append(calls, Call{Caller: caller, Target: match[1], Line: i + 1, Kind: "signal_connect"})
		}
	}
	return calls
}

func parseReferences(lines []string, path string) []Reference {
	references := make([]Reference, 0, 16)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if gdClassNameRE.MatchString(line) || gdFunctionRE.MatchString(line) || gdSignalRE.MatchString(line) || strings.HasPrefix(trimmed, "#") {
			continue
		}
		code := codeOnly(line)
		for _, match := range gdIdentifierRE.FindAllString(code, -1) {
			kind := "identifier"
			if strings.Contains(code, match+".connect") || strings.Contains(code, match+" .connect") || strings.Contains(code, match+".emit") || strings.Contains(code, match+" .emit") {
				if strings.Contains(code, ".connect") {
					kind = "signal_connect"
				} else {
					kind = "signal_emit"
				}
			}
			for _, callback := range gdConnectRE.FindAllStringSubmatch(code, -1) {
				if strings.EqualFold(match, callback[1]) {
					kind = "signal_connect"
				}
			}
			for _, callback := range gdCallableNameRE.FindAllStringSubmatch(stripComment(line), -1) {
				if strings.EqualFold(match, callback[1]) {
					kind = "signal_connect"
				}
			}
			references = append(references, Reference{Name: match, Path: path, Line: i + 1, Kind: kind})
		}
	}
	return references
}

func functionEndLine(lines []string, start int, indent int) int {
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if indentationWidth(lines[i]) <= indent {
			return i
		}
	}
	return len(lines)
}

func indentationWidth(line string) int {
	indent := 0
	for _, char := range line {
		switch char {
		case ' ':
			indent++
		case '\t':
			indent += 4
		default:
			return indent
		}
	}
	return indent
}

func codeOnly(line string) string {
	line = stripComment(line)
	var builder strings.Builder
	quote := rune(0)
	escaped := false
	for _, char := range line {
		if quote != 0 {
			builder.WriteByte(' ')
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
			} else if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			builder.WriteByte(' ')
			continue
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

func stripComment(line string) string {
	quote := rune(0)
	escaped := false
	for i, char := range line {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
			} else if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if char == '#' {
			return line[:i]
		}
	}
	return line
}

func resolveResourcePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "res://") {
		return ""
	}
	target := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(raw, "res://")))
	if target == "." || target == ".." || strings.HasPrefix(target, "../") || filepath.Ext(target) != ".gd" {
		return ""
	}
	return target
}
