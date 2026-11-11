package cpp

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
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
	// cppNamespaceAliasRe finds `namespace X = Y;` at file or block scope.
	cppNamespaceAliasRe = regexp.MustCompile(`^namespace\s+([A-Za-z_]\w*)\s*=\s*((?:::)?[A-Za-z_]\w*(?:\s*::\s*[A-Za-z_]\w*)*)\s*;`)
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
func declaredTypeBefore(text string, nameStart int, term byte, body bool) string {
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

	if !atDeclarationBoundary(text, i, term, body) {
		return ""
	}

	return text[i:end]
}

var cppDeclQualifiers = []string{"const", "volatile", "static", "constexpr", "inline", "mutable", "typename", "struct", "class", "register", "thread_local"}

// atDeclarationBoundary reports whether the type starting at typeStart begins a
// declaration rather than sitting inside an expression (`a && k`, `s * q`).
// In a function body only statement starts and control-statement heads count;
// in a signature a parameter may follow `(` or `,`.
func atDeclarationBoundary(text string, typeStart int, term byte, body bool) bool {
	i := skipSpacesBack(text, typeStart)

	for stripped := true; stripped; {
		stripped = false

		for _, word := range cppDeclQualifiers {
			if strings.HasSuffix(text[:i], word) && (i == len(word) || !isIdentByte(text[i-len(word)-1])) {
				i = skipSpacesBack(text, i-len(word))
				stripped = true
			}
		}
	}

	if i == 0 {
		return true
	}

	switch text[i-1] {
	case '{', ';':
		return true
	case ',':
		// in a body, only a lambda's parameter list declares after a comma
		return !body || isLambdaParamList(text, i-1)
	case '<':
		return !body
	case '(':
		if !body {
			return true
		}

		head := skipSpacesBack(text, i-1)
		if head > 0 && text[head-1] == ']' {
			return true
		}

		for _, word := range []string{"if", "for", "while", "switch", "catch"} {
			if strings.HasSuffix(text[:head], word) && (head == len(word) || !isIdentByte(text[head-len(word)-1])) {
				// catch (T name) ends its one declaration with ')'
				return term == '=' || term == ':' || term == ';' || term == ')' && word == "catch"
			}
		}
	}

	return false
}

// isLambdaParamList reports whether the comma at text[comma] separates the
// parameters of a lambda (`[...](A a, B b)` or `[...]<class T>(T a, B b)`).
func isLambdaParamList(text string, comma int) bool {
	depth := 0

	for i := comma - 1; i >= 0; i-- {
		switch text[i] {
		case ')':
			depth++
		case '(':
			if depth--; depth < 0 {
				head := skipSpacesBack(text, i)

				return head > 0 && (text[head-1] == ']' || text[head-1] == '>')
			}
		}
	}

	return false
}

// isParenDeclared reports whether the declaration at lines[line-1][at:] sits in
// a parenthesised head or parameter list: an unmatched ')' comes before any
// '{' or '}' that opens or closes a block (a few lines at most).
func isParenDeclared(lines []string, line int, at int, endLine int) bool {
	depth := 0

	for current := line; current <= endLine && current <= len(lines) && current < line+4; current++ {
		text := indexsupport.StripLineComment(lines[current-1])
		if current == line {
			text = text[min(at, len(text)):]
		}

		for i := 0; i < len(text); i++ {
			switch text[i] {
			case '(':
				depth++
			case ')':
				if depth--; depth < 0 {
					return true
				}
			case '{', '}':
				if depth == 0 {
					return false
				}
			}
		}
	}

	return false
}

func before(text string, i int) byte {
	if i = skipSpacesBack(text, i); i > 0 {
		return text[i-1]
	}

	return 0
}

// cppAmbiguousType marks a name declared with conflicting types.
const cppAmbiguousType = "?"

// declaredVarsIn records the declared type of each wanted name that is
// declared in text (the name is followed by a declarator terminator).
// Redeclared names keep the last type; shadowing is not modelled.
func declaredVarsIn(vars map[string]string, text string, want map[string]bool, body bool) map[string]string {
	for _, decl := range declarationsIn(text, want, body) {
		if vars == nil {
			vars = make(map[string]string, 4)
		}

		// A name declared with two different types (shadowing, or a
		// look-alike expression) is not trusted to either.
		typeName := decl.typeText
		if prior, seen := vars[decl.name]; seen && prior != typeName {
			typeName = cppAmbiguousType
		}

		vars[decl.name] = typeName
	}

	return vars
}

// cppDeclaration is one `Type name` declaration found on a line.
type cppDeclaration struct {
	name     string
	typeText string
	at       int // byte offset of the name in the line
}

// declarationsIn returns the declarations of the wanted names on one line.
func declarationsIn(text string, want map[string]bool, body bool) []cppDeclaration {
	var decls []cppDeclaration

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

			if typeName := declaredTypeBefore(text, start, rest[0], body); typeName != "" {
				decls = append(decls, cppDeclaration{name: name, typeText: typeName, at: start})
			}
		}
	}

	return decls
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

	locals := localFuncNames(calls)

	if want == nil && locals == nil {
		return nil
	}

	vars := declaredVarsIn(nil, signature, want, false)

	for line := startLine; line <= endLine && line <= len(lines); line++ {
		text := indexsupport.StripLineComment(lines[line-1])
		if len(text) < 4 {
			continue
		}

		if mentionsAny(text, want) {
			for _, decl := range declarationsIn(text, want, true) {
				if vars == nil {
					vars = make(map[string]string, 4)
				}

				end := localScopeEnd(lines, line, decl.at, endLine)
				if isParenDeclared(lines, line, decl.at, endLine) {
					end = parenScopeEnd(lines, line, decl.at, endLine)
				}

				vars[cppScopedVarPrefix+decl.name] += strconv.Itoa(line) + "-" + strconv.Itoa(end) + "=" + decl.typeText + "\n"
			}
		}

		for name := range locals {
			if at := assignsLocal(text, name); at >= 0 {
				if vars == nil {
					vars = make(map[string]string, 4)
				}

				span := strconv.Itoa(line) + "-" + strconv.Itoa(localScopeEnd(lines, line, at, endLine))
				if prior := vars[cppLocalFuncPrefix+name]; prior != "" {
					span = prior + "," + span
				}

				vars[cppLocalFuncPrefix+name] = span
			}
		}
	}

	return vars
}

// cppScopedVarPrefix keys Vars entries for receivers declared in the body:
// one "from-to=type" line per declaration, in scope from its line to the end
// of its block. Parameters stay under the bare name for the whole function.
const cppScopedVarPrefix = "at:"

// scopedDecl is one body declaration of a receiver in scope at a line;
// boundary is true when the line holds its first or last line, where a call
// may sit before the declaration or after its scope closes.
type scopedDecl struct {
	typeText string
	from     int
	boundary bool
}

// scopedVarDecls returns the body declarations of a receiver whose span covers
// line, innermost (latest) first.
func scopedVarDecls(entries string, line int) []scopedDecl {
	if entries == "" {
		return nil
	}

	var decls []scopedDecl

	for _, entry := range strings.Split(strings.TrimSuffix(entries, "\n"), "\n") {
		span, entryType, found := strings.Cut(entry, "=")
		if !found || !localInScope(span, line) {
			continue
		}

		from, to, _ := strings.Cut(span, "-")
		first, errFrom := strconv.Atoi(from)
		last, errTo := strconv.Atoi(to)

		if errFrom == nil && errTo == nil {
			decls = append(decls, scopedDecl{typeText: entryType, from: first, boundary: line == first || line == last})
		}
	}

	slices.SortStableFunc(decls, func(a, b scopedDecl) int { return cmp.Compare(b.from, a.from) })

	return decls
}

// parenScopeEnd returns the last line where a name declared inside parentheses
// at lines[line-1][at:] is in scope: the {} body or the single statement that
// follows the closing parenthesis (lambda parameters, for/if/while/catch heads).
func parenScopeEnd(lines []string, line int, at int, endLine int) int {
	parens, braces, afterHead := 0, 0, false

	for current := line; current <= endLine && current <= len(lines); current++ {
		text := indexsupport.StripLineComment(lines[current-1])
		if current == line {
			text = text[min(at, len(text)):]
		}

		for i := 0; i < len(text); i++ {
			c := text[i]

			switch {
			case !afterHead && c == '(':
				parens++
			case !afterHead && c == ')':
				if parens--; parens < 0 {
					afterHead = true
				}
			case !afterHead:
			case c == '{':
				braces++
			case c == '}':
				if braces--; braces <= 0 {
					return current
				}
			case c == ';' && braces == 0:
				return current
			}
		}
	}

	return endLine
}

// cppLocalFuncPrefix keys Vars entries that record a local variable (often a
// lambda) shadowing a plain call's name, so `run(...)` on it is not a member
// call. The value lists the line spans where the local is in scope, as
// "from-to[,from-to...]": from its declaration line to the end of its block.
const cppLocalFuncPrefix = "fn:"

// localScopeEnd returns the last line of the block that holds a declaration at
// lines[line-1][at:], i.e. where the brace depth from that point goes negative.
func localScopeEnd(lines []string, line int, at int, endLine int) int {
	depth := 0

	for current := line; current <= endLine && current <= len(lines); current++ {
		text := indexsupport.StripLineComment(lines[current-1])
		if current == line {
			text = text[min(at, len(text)):]
		}

		for i := 0; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				if depth--; depth < 0 {
					return current
				}
			}
		}
	}

	return endLine
}

// localInScope reports whether the local spans recorded under
// cppLocalFuncPrefix cover line.
func localInScope(spans string, line int) bool {
	for _, span := range strings.Split(spans, ",") {
		from, to, ok := strings.Cut(span, "-")
		if !ok {
			continue
		}

		first, errFrom := strconv.Atoi(from)
		last, errTo := strconv.Atoi(to)

		if errFrom == nil && errTo == nil && line >= first && line <= last {
			return true
		}
	}

	return false
}

func localFuncNames(calls []CppCall) map[string]bool {
	var names map[string]bool

	for _, call := range calls {
		if call.Via != callViaPlain {
			continue
		}

		if names == nil {
			names = make(map[string]bool, 4)
		}

		names[call.Name] = true
	}

	return names
}

// assignsLocal returns where text initialises a variable called name
// (`auto name = ...`, `std::function<void()> name{...}`), or -1.
func assignsLocal(text string, name string) int {
	for pos := 0; ; {
		at := strings.Index(text[pos:], name)
		if at < 0 {
			return -1
		}

		start := pos + at
		pos = start + len(name)

		if start > 0 && isIdentByte(text[start-1]) || start == 0 || pos >= len(text) {
			continue
		}

		rest := strings.TrimLeft(text[pos:], " \t")
		if strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "==") && declaredTypeBefore(text, start, '=', true) != "" {
			return start
		}

		if strings.HasPrefix(rest, "{") && declaredTypeBefore(text, start, '{', true) != "" {
			return start
		}
	}
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
			if from, name, ok := usingDeclaration(text); ok {
				if fields == nil {
					fields = make(map[string]string, 4)
				}

				fields[cppUsingPrefix+name] = from
			} else {
				fields = fieldDeclIn(fields, text)
			}
		}

		depth += strings.Count(text, "{") - strings.Count(text, "}")
	}

	return fields
}

// cppUsingPrefix keys class Fields entries that record `using Base::name;`,
// which brings Base's member into the class instead of hiding it.
const cppUsingPrefix = "using:"

// usingDeclaration reads `using X::name;` into X and name.
func usingDeclaration(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "using ") || strings.Contains(text, "=") || strings.HasPrefix(text, "using namespace ") {
		return "", "", false
	}

	qualified := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "using "), ";"))
	qualified = strings.TrimSpace(strings.TrimPrefix(qualified, "typename "))

	cut := strings.LastIndex(qualified, "::")
	if cut <= 0 {
		return "", "", false
	}

	return qualified[:cut], qualified[cut+2:], true
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

// parseCppAliases reads `using A = B;` and `typedef B A;` into alias -> target,
// with the file's namespace aliases expanded in the target.
func parseCppAliases(content string, aliases map[string]string, namespaces map[string]string) {
	if !strings.Contains(content, "using ") && !strings.Contains(content, "typedef ") {
		return
	}

	for _, match := range cppAliasUsingRe.FindAllStringSubmatch(content, -1) {
		addCppAlias(aliases, match[1], expandCppNamespaceAliases(strings.TrimSpace(match[2]), namespaces))
	}

	for _, match := range cppAliasTypedef.FindAllStringSubmatch(content, -1) {
		addCppAlias(aliases, match[2], expandCppNamespaceAliases(strings.TrimSpace(match[1]), namespaces))
	}
}

// parseCppNamespaceAliases reads one file's `namespace X = Y;` declarations
// (file or block scope; both apply to the whole file) into X -> Y, with earlier
// aliases expanded in Y. A name aliased to two targets maps to "" and is left
// unexpanded, so the resolver fails closed on it.
func parseCppNamespaceAliases(content string) map[string]string {
	var aliases map[string]string

	for pos := 0; ; {
		at := strings.Index(content[pos:], "namespace")
		if at < 0 {
			break
		}

		start := pos + at
		pos = start + len("namespace")

		// Only run the regex where a `namespace name =` head is present; most
		// namespace lines open a body and are skipped by this byte scan.
		if start > 0 && isIdentByte(content[start-1]) || !cppNamespaceAliasHead(content, pos) {
			continue
		}

		match := cppNamespaceAliasRe.FindStringSubmatch(content[start:min(len(content), start+512)])
		if match == nil {
			continue
		}

		if aliases == nil {
			aliases = make(map[string]string, 2)
		}

		target := strings.TrimPrefix(strings.Join(strings.Fields(match[2]), ""), "::")
		target = expandCppNamespaceAliases(target, aliases)

		if prior, seen := aliases[match[1]]; seen && prior != target {
			target = ""
		}

		aliases[match[1]] = target
	}

	return aliases
}

// cppNamespaceAliasHead reports whether content[pos:] reads ` name =`.
func cppNamespaceAliasHead(content string, pos int) bool {
	i := pos
	for i < len(content) && (content[i] == ' ' || content[i] == '\t') {
		i++
	}

	if i == pos {
		return false
	}

	nameStart := i
	for i < len(content) && isIdentByte(content[i]) {
		i++
	}

	for i < len(content) && (content[i] == ' ' || content[i] == '\t') {
		i++
	}

	return i > nameStart && i < len(content) && content[i] == '='
}

// expandCppNamespaceAliases rewrites every `X::` in text whose X is a namespace
// alias (and not itself qualified, as in a::X::) to the alias target.
func expandCppNamespaceAliases(text string, aliases map[string]string) string {
	for alias, target := range aliases {
		if target == "" || !strings.Contains(text, alias+"::") {
			continue
		}

		var out strings.Builder

		for pos := 0; ; {
			at := strings.Index(text[pos:], alias+"::")
			if at < 0 {
				out.WriteString(text[pos:])

				break
			}

			start := pos + at
			out.WriteString(text[pos:start])
			pos = start + len(alias)

			if start > 0 && (isIdentByte(text[start-1]) || text[start-1] == ':') {
				out.WriteString(alias)
			} else {
				out.WriteString(target)
			}
		}

		text = out.String()
	}

	return text
}

// applyCppNamespaceAliases expands a file's namespace aliases in the type and
// scope text its symbols carry (bases, fields, receiver types, call qualifiers),
// so the resolver sees ks::Kernel as proj::sim::Kernel.
func applyCppNamespaceAliases(symbols []CppSymbol, aliases map[string]string) {
	if len(aliases) == 0 {
		return
	}

	for si := range symbols {
		symbol := &symbols[si]

		for i, base := range symbol.Bases {
			symbol.Bases[i] = expandCppNamespaceAliases(base, aliases)
		}

		for name, typeText := range symbol.Fields {
			symbol.Fields[name] = expandCppNamespaceAliases(typeText, aliases)
		}

		for name, typeText := range symbol.Vars {
			symbol.Vars[name] = expandCppNamespaceAliases(typeText, aliases)
		}

		for i := range symbol.Calls {
			// A qualifier is written without its trailing "::" (occ in occ::f()).
			if qualifier := symbol.Calls[i].Qualifier; qualifier != "" && qualifier != "?" {
				symbol.Calls[i].Qualifier = strings.TrimSuffix(expandCppNamespaceAliases(qualifier+"::", aliases), "::")
			}
		}
	}
}

// addCppAlias records alias -> target; the map is keyed by bare name, so the
// same name aliased to different targets is marked ambiguous, not overwritten.
func addCppAlias(aliases map[string]string, name string, target string) {
	if prior, seen := aliases[name]; seen && prior != target {
		target = cppAmbiguousType
	}

	aliases[name] = target
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

	if typeName := declaredTypeBefore(text, start, ';', false); typeName != "" {
		if fields == nil {
			fields = make(map[string]string, 4)
		}

		fields[text[start:end]] = typeName
	}

	return fields
}
