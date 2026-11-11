package cpp

import (
	"regexp"
	"strings"

	"idiom/internal/repository/semantic/internal/indexsupport"
)

// Call forms recorded for every call site. The syntax index has no compiler, so
// it keeps the lexical shape of the callee (how the name was reached) and the
// declared types it can read cheaply (parameters, locals, fields, bases); the
// query then resolves a call against one owner or reports it unresolved.
const (
	callViaPlain     = ""   // run(...)
	callViaQualified = "::" // Owner::run(...)
	callViaDot       = "."  // x.run(...)
	callViaArrow     = "->" // x->run(...)
)

var (
	// cppBaseClauseRe finds the base-specifier list of a class head.
	cppBaseClauseRe = regexp.MustCompile(`^[^{;:]*?(?:\bfinal\b\s*)?:[^:]([^{;]*)`)
	cppAliasUsingRe = regexp.MustCompile(`(?m)^\s*using\s+([A-Za-z_]\w*)\s*=\s*([^;]+);`)
	cppAliasTypedef = regexp.MustCompile(`(?m)^\s*typedef\s+([^;(]+?)\s+([A-Za-z_]\w*)\s*;`)
)

// extractCppCalls records every call in lines[startLine..endLine] with the form
// it was reached by.
func extractCppCalls(lines []string, startLine int, endLine int) []CppCall {
	if startLine <= 0 || endLine < startLine || endLine > len(lines) {
		return nil
	}

	calls := make([]CppCall, 0, 16)

	for line := startLine; line <= endLine; line++ {
		text := indexsupport.StripLineComment(lines[line-1])
		if !strings.Contains(text, "(") {
			continue
		}

		for _, loc := range cppCallRe.FindAllStringSubmatchIndex(text, -1) {
			name := text[loc[2]:loc[3]]
			if isCppKeywordName(name) {
				continue
			}

			call := CppCall{Name: name, Line: line}
			call.Via, call.Qualifier, call.Receiver = cppCallContext(text, loc[2])
			calls = append(calls, call)
		}
	}

	return calls
}

// cppCallContext reads backwards from the callee name to see how it was
// reached. Receiver is the plain identifier an object is called through
// ("this" for this->; this->m is reported as m); it is empty for a member call whose receiver is a more complex expression.
// Qualifier is the written scope of a qualified call; "?" marks a scope the
// scanner cannot read (a template-id).
func cppCallContext(text string, nameStart int) (via string, qualifier string, receiver string) {
	i := skipSpacesBack(text, nameStart)

	switch {
	case strings.HasSuffix(text[:i], "::"):
		qualifier = scanQualifierBack(text, i-2)

		return callViaQualified, qualifier, ""
	case strings.HasSuffix(text[:i], "->"):
		return callViaArrow, "", scanReceiverBack(text, i-2)
	case strings.HasSuffix(text[:i], ".") && !(i >= 2 && text[i-2] >= '0' && text[i-2] <= '9'):
		return callViaDot, "", scanReceiverBack(text, i-1)
	}

	return callViaPlain, "", ""
}

func skipSpacesBack(text string, end int) int {
	for end > 0 && (text[end-1] == ' ' || text[end-1] == '\t') {
		end--
	}

	return end
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// scanQualifierBack reads the Scope::Scope chain that ends before end.
func scanQualifierBack(text string, end int) string {
	end = skipSpacesBack(text, end)
	if end == 0 {
		return ""
	}

	if text[end-1] == '>' {
		return "?"
	}

	var parts []string

	for {
		start := end
		for start > 0 && isIdentByte(text[start-1]) {
			start--
		}

		if start == end {
			break
		}

		parts = append([]string{text[start:end]}, parts...)

		before := skipSpacesBack(text, start)
		if !strings.HasSuffix(text[:before], "::") {
			break
		}

		end = skipSpacesBack(text, before-2)
		if end > 0 && text[end-1] == '>' {
			return "?"
		}
	}

	return strings.Join(parts, "::")
}

// scanReceiverBack returns the identifier called through, or "" when the
// receiver is not a bare identifier (call result, index, chained member).
func scanReceiverBack(text string, end int) string {
	end = skipSpacesBack(text, end)
	start := end

	for start > 0 && isIdentByte(text[start-1]) {
		start--
	}

	if start == end || text[start] >= '0' && text[start] <= '9' {
		return ""
	}

	receiver := text[start:end]

	before := skipSpacesBack(text, start)
	switch {
	case strings.HasSuffix(text[:before], "::"):
		return ""
	case strings.HasSuffix(text[:before], "->"):
		if receiverIsThis(text, before-2) {
			return receiver
		}

		return ""
	case strings.HasSuffix(text[:before], "."):
		return ""
	}

	return receiver
}

func receiverIsThis(text string, end int) bool {
	end = skipSpacesBack(text, end)

	return strings.HasSuffix(text[:end], "this") && (end == 4 || !isIdentByte(text[end-5]))
}

var cppNonTypeWords = map[string]bool{
	"return": true, "else": true, "case": true, "goto": true, "delete": true, "throw": true, "new": true,
	"using": true, "namespace": true, "typedef": true, "co_return": true, "co_await": true, "co_yield": true,
	"sizeof": true, "do": true, "public": true, "private": true, "protected": true, "template": true,
	"operator": true, "friend": true, "virtual": true, "explicit": true, "default": true,
}

// declaredTypeBefore reads the type written before the declarator name that
// starts at nameStart ("const Foo* name", "std::vector<Foo>& name"), or "" if
// the text before the name is not a type.
func declaredTypeBefore(text string, nameStart int) string {
	i := skipSpacesBack(text, nameStart)

	for {
		switch {
		case i > 0 && (text[i-1] == '*' || text[i-1] == '&'):
			i = skipSpacesBack(text, i-1)

			continue
		case strings.HasSuffix(text[:i], "const") && (i == 5 || !isIdentByte(text[i-6])):
			i = skipSpacesBack(text, i-5)

			continue
		}

		break
	}

	if i == nameStart || i == 0 {
		return ""
	}

	end := i

	if text[i-1] == '>' {
		depth := 0

		for i > 0 {
			i--

			if text[i] == '>' {
				depth++
			} else if text[i] == '<' {
				if depth--; depth == 0 {
					break
				}
			}
		}

		if depth != 0 {
			return ""
		}
	}

	for i > 0 && (isIdentByte(text[i-1]) || text[i-1] == ':') {
		i--
	}

	if i == end || before(text, i) == '.' || before(text, i) == '>' || cppNonTypeWords[text[i:end]] || isCppKeywordName(text[i:end]) {
		return ""
	}

	return text[i:end]
}

func before(text string, i int) byte {
	if i = skipSpacesBack(text, i); i > 0 {
		return text[i-1]
	}

	return 0
}

// declaredVarsIn records the declared type of each wanted name that is
// declared in text (the name is followed by a declarator terminator).
// Redeclared names keep the last type; shadowing is not modelled.
func declaredVarsIn(vars map[string]string, text string, want map[string]bool) map[string]string {
	for name := range want {
		for pos := 0; pos < len(text); {
			at := strings.Index(text[pos:], name)
			if at < 0 {
				break
			}

			start := pos + at
			pos = start + len(name)

			if start > 0 && isIdentByte(text[start-1]) || pos < len(text) && isIdentByte(text[pos]) {
				continue
			}

			rest := strings.TrimLeft(text[pos:], " \t")
			if rest == "" || !strings.ContainsRune(";=,){([", rune(rest[0])) && !(rest[0] == ':' && !strings.HasPrefix(rest, "::")) {
				continue
			}

			if typeName := declaredTypeBefore(text, start); typeName != "" {
				if vars == nil {
					vars = make(map[string]string, 4)
				}

				vars[name] = typeName
			}
		}
	}

	return vars
}

// collectCppBodyVars gathers the declarations of the receivers that calls go
// through, from a function signature and body. Only lines that mention a
// receiver are matched, which keeps indexing cost near the plain call scan.
func collectCppBodyVars(signature string, lines []string, startLine int, endLine int, calls []CppCall) map[string]string {
	var want map[string]bool

	for _, call := range calls {
		if call.Receiver == "" || call.Receiver == "this" {
			continue
		}

		if want == nil {
			want = make(map[string]bool, 4)
		}

		want[call.Receiver] = true
	}

	if want == nil {
		return nil
	}

	vars := declaredVarsIn(nil, signature, want)

	for line := startLine; line <= endLine && line <= len(lines); line++ {
		text := indexsupport.StripLineComment(lines[line-1])
		if len(text) < 4 || !mentionsAny(text, want) {
			continue
		}

		vars = declaredVarsIn(vars, text, want)
	}

	return vars
}

func mentionsAny(text string, names map[string]bool) bool {
	for name := range names {
		if strings.Contains(text, name) {
			return true
		}
	}

	return false
}

// collectCppFields gathers data-member declarations that sit directly inside
// the class body that spans lines[start..end] (nested bodies are skipped).
func collectCppFields(lines []string, startLine int, endLine int) map[string]string {
	var fields map[string]string

	depth := 0

	for line := startLine; line <= endLine && line <= len(lines); line++ {
		text := indexsupport.StripLineComment(lines[line-1])
		if depth == 1 && strings.HasSuffix(strings.TrimSpace(text), ";") && !strings.Contains(text, "(") {
			fields = fieldDeclIn(fields, text)
		}

		depth += strings.Count(text, "{") - strings.Count(text, "}")
	}

	return fields
}

// parseCppBases returns the base class names written in the head of the type
// declared at lines[start] (the head may continue over a few lines).
func parseCppBases(lines []string, start int) []string {
	var head strings.Builder

	for i := start; i < len(lines) && i < start+6; i++ {
		clean := indexsupport.StripLineComment(lines[i])
		head.WriteString(clean)
		head.WriteByte(' ')

		if strings.ContainsAny(clean, "{;") {
			break
		}
	}

	match := cppBaseClauseRe.FindStringSubmatch(head.String())
	if len(match) != 2 {
		return nil
	}

	var bases []string

	depth, begin := 0, 0
	clause := match[1]

	flush := func(end int) {
		if base := cleanCppBase(clause[begin:end]); base != "" {
			bases = append(bases, base)
		}
	}

	for i := 0; i < len(clause); i++ {
		switch clause[i] {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				flush(i)

				begin = i + 1
			}
		}
	}

	flush(len(clause))

	return bases
}

func cleanCppBase(spec string) string {
	fields := strings.Fields(spec)
	for len(fields) > 0 {
		switch fields[0] {
		case "public", "protected", "private", "virtual":
			fields = fields[1:]

			continue
		}

		break
	}

	if len(fields) == 0 {
		return ""
	}

	base, _ := splitCppType(fields[0])

	return base
}

// splitCppType strips qualifiers, pointers, references and a leading ::, and
// splits a template-id into its base name and argument text.
func splitCppType(typeName string) (base string, args string) {
	typeName = strings.TrimSpace(typeName)
	for _, word := range []string{"const ", "volatile ", "struct ", "class ", "typename "} {
		typeName = strings.TrimPrefix(typeName, word)
	}

	typeName = strings.TrimLeft(typeName, ":")

	if open := strings.Index(typeName, "<"); open >= 0 {
		if closeAt := strings.LastIndex(typeName, ">"); closeAt > open {
			args = typeName[open+1 : closeAt]
		}

		typeName = typeName[:open]
	}

	return strings.TrimRight(strings.TrimSpace(typeName), "*& "), args
}

// parseCppAliases reads `using A = B;` and `typedef B A;` into alias -> target.
func parseCppAliases(content string, aliases map[string]string) {
	if !strings.Contains(content, "using ") && !strings.Contains(content, "typedef ") {
		return
	}

	for _, match := range cppAliasUsingRe.FindAllStringSubmatch(content, -1) {
		aliases[match[1]] = strings.TrimSpace(match[2])
	}

	for _, match := range cppAliasTypedef.FindAllStringSubmatch(content, -1) {
		aliases[match[2]] = strings.TrimSpace(match[1])
	}
}

// fieldDeclIn records the member declared by one `Type name [= init];` line.
func fieldDeclIn(fields map[string]string, text string) map[string]string {
	if cut := strings.IndexAny(text, "={;"); cut >= 0 {
		text = text[:cut]
	}

	end := len(strings.TrimRight(text, " \t"))
	start := end

	for start > 0 && isIdentByte(text[start-1]) {
		start--
	}

	if start == end {
		return fields
	}

	if typeName := declaredTypeBefore(text, start); typeName != "" {
		if fields == nil {
			fields = make(map[string]string, 4)
		}

		fields[text[start:end]] = typeName
	}

	return fields
}
