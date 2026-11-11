package cpp

import (
	"cmp"
	"slices"
	"strings"
)

// CppUnresolvedCall is a call to the owner's name whose receiver or scope the
// syntax index could not read; it may or may not reach the owner, so it is
// reported apart from callers rather than guessed.
type CppUnresolvedCall struct {
	Caller     CppSymbolMatch
	CalleeName string
	Line       int
	Reason     string
}

// CppCallers is the answer to "who calls this owner". Callers and Unresolved
// are limited to the requested count; the totals are before truncation.
type CppCallers struct {
	Callers         []CppCallMatch
	Total           int
	Unresolved      []CppUnresolvedCall
	UnresolvedTotal int
}

// callSite identifies one call line; several calls to the same name on a line
// (or a call repeated by overlapping symbols) are reported once.
type callSite struct {
	path string
	line int
}

type callVerdict int

const (
	verdictNone callVerdict = iota
	verdictCaller
	verdictUnresolved
)

// callResolver classifies call sites against one resolved owner. It is built
// once per query from the class table (bases, fields) and the alias map.
type callResolver struct {
	idx      *CppIndex
	name     string
	owner    string
	scope    string
	isType   bool
	isMember bool
	// sub holds the owner class and every class that inherits the owner's
	// method without redeclaring it.
	sub       []string
	classes   map[string][]*CppSymbol // last segment -> class symbols
	methodsOf []string                // scopes that declare a method called name
	// declarers are the classes methodsOf resolves to; unsureDeclarers are
	// method scopes that match several classes (an out-of-class definition
	// after `using namespace`), which never prove hiding.
	declarers       []string
	unsureDeclarers []string
	known           map[string]bool // first namespace segment of every indexed symbol
	// namespaces maps a namespace's last segment to its qualified names.
	namespaces map[string][]string
}

func lastSegment(qualified string) string {
	if cut := strings.LastIndex(qualified, "::"); cut >= 0 {
		return qualified[cut+2:]
	}

	return qualified
}

// scopesCompatible reports whether two possibly partially qualified names can
// denote the same entity (a.b.C vs C, as an out-of-class definition or a using
// directive leaves the namespace implicit).
func scopesCompatible(a string, b string) bool {
	return a == b || strings.HasSuffix(a, "::"+b) || strings.HasSuffix(b, "::"+a)
}

func newCallResolver(idx *CppIndex, res CppResolution) *callResolver {
	r := &callResolver{idx: idx, name: res.Name, owner: res.Owner, classes: make(map[string][]*CppSymbol), known: make(map[string]bool), namespaces: make(map[string][]string)}

	for _, def := range res.Definitions {
		if isCppContainerKind(def.Kind) {
			r.isType = true
		}

		r.scope = def.Scope
	}

	var all []*CppSymbol

	for fi := range idx.Files {
		symbols := idx.Files[fi].Symbols
		for si := range symbols {
			symbol := &symbols[si]

			if symbol.Scope != "" {
				r.known[firstSegment(symbol.Scope)] = true
			} else if symbol.Kind == "namespace" || isCppContainerKind(symbol.Kind) {
				r.known[symbol.Name] = true
			}

			switch {
			case symbol.Kind == "namespace":
				r.namespaces[symbol.Name] = appendUniqueString(r.namespaces[symbol.Name], symbol.Qualified)
			case symbol.Kind == "class" || symbol.Kind == "struct" || symbol.Kind == "union":
				r.classes[symbol.Name] = append(r.classes[symbol.Name], symbol)
				all = append(all, symbol)
			case symbol.Name == r.name && symbol.Scope != "" && symbol.Kind == "method":
				r.methodsOf = append(r.methodsOf, symbol.Scope)
			}
		}
	}

	for _, scope := range r.methodsOf {
		switch status, qualified := r.resolveType(scope, ""); status {
		case typeFound:
			r.declarers = appendUniqueString(r.declarers, qualified)
		case typeAmbiguous:
			r.unsureDeclarers = appendUniqueString(r.unsureDeclarers, scope)
		default:
			r.declarers = appendUniqueString(r.declarers, scope)
		}
	}

	if r.isType {
		return r
	}

	for _, class := range r.classes[lastSegment(r.scope)] {
		if scopesCompatible(class.Qualified, r.scope) {
			r.isMember = true
			r.sub = appendUniqueString(r.sub, class.Qualified)
		}
	}

	if !r.isMember {
		return r
	}

	for changed := true; changed; {
		changed = false

		for _, class := range all {
			if declares, unsure := r.declaresMethod(class.Qualified); declares || unsure || slices.Contains(r.sub, class.Qualified) {
				continue
			}

			for _, base := range class.Bases {
				if status, qualified := r.resolveType(base, class.Qualified); status == typeFound && slices.Contains(r.sub, qualified) {
					r.sub = append(r.sub, class.Qualified)
					changed = true

					break
				}
			}
		}
	}

	return r
}

func appendUniqueString(list []string, value string) []string {
	if slices.Contains(list, value) {
		return list
	}

	return append(list, value)
}

// declaresMethod reports whether class declares a member called name, and
// whether an ambiguous out-of-class definition might (unsure: no proof).
func (r *callResolver) declaresMethod(class string) (declares bool, unsure bool) {
	for _, scope := range r.declarers {
		if scopesCompatible(scope, class) {
			return true, false
		}
	}

	for _, scope := range r.unsureDeclarers {
		if scopesCompatible(scope, class) {
			return false, true
		}
	}

	return false, false
}

// usingVerdict decides a class that brings Base::name in with a using-
// declaration: its own overloads do not hide Base's, so a call may reach Base.
func (r *callResolver) usingVerdict(class *CppSymbol, depth int) (callVerdict, string, bool) {
	from, ok := class.Fields[cppUsingPrefix+r.name]
	if !ok {
		return verdictNone, "", false
	}

	if status, qualified := r.resolveType(from, class.Qualified); status == typeFound {
		if verdict, _ := r.inheritVerdict(qualified, depth+1); verdict == verdictCaller {
			return verdictCaller, "", true
		}
	}

	return verdictUnresolved, "class `" + class.Qualified + "` brings `" + from + "::" + r.name + "` in with a using-declaration beside its own overloads", true
}

func firstSegment(qualified string) string {
	qualified = strings.TrimPrefix(qualified, "::")
	if cut := strings.Index(qualified, "::"); cut >= 0 {
		return qualified[:cut]
	}

	return qualified
}

const (
	typeNone = iota
	typeFound
	typeAmbiguous
)

// resolveType maps a written class name to one indexed class, preferring the
// class an enclosing namespace of ctx (the caller's scope) selects. A name that
// several indexed classes share and no enclosing namespace picks is ambiguous.
func (r *callResolver) resolveType(name string, ctx string) (int, string) {
	name = r.expandNamespaceAlias(strings.TrimPrefix(name, "::"))

	var candidates []string

	for _, class := range r.classes[lastSegment(name)] {
		if scopesCompatible(class.Qualified, name) {
			candidates = appendUniqueString(candidates, class.Qualified)
		}
	}

	switch {
	case len(candidates) == 0:
		return typeNone, ""
	case len(candidates) == 1:
		return typeFound, candidates[0]
	case slices.Contains(candidates, name):
		return typeFound, name
	}

	for scope := ctx; ; {
		if want := joinCppScope(scope, name); slices.Contains(candidates, want) {
			return typeFound, want
		}

		if scope == "" {
			break
		}

		if cut := strings.LastIndex(scope, "::"); cut >= 0 {
			scope = scope[:cut]
		} else {
			scope = ""
		}
	}

	return typeAmbiguous, ""
}

// expandNamespaceAlias expands a first segment that is no indexed namespace but
// a namespace alias declared in some other file (a header the caller includes).
func (r *callResolver) expandNamespaceAlias(name string) string {
	first := firstSegment(name)
	if r.known[first] {
		return name
	}

	if target := r.idx.NamespaceAliases[first]; target != "" {
		return target + name[len(first):]
	}

	return name
}

func (r *callResolver) ambiguousReason(name string) string {
	return "class name `" + name + "` matches several indexed classes and the enclosing namespace does not pick one"
}

// resolveAlias follows using/typedef names to their target, a few levels deep.
func (r *callResolver) resolveAlias(base string, args string) (string, string) {
	for range 4 {
		target, ok := r.idx.Aliases[lastSegment(base)]
		if !ok {
			break
		}

		next, nextArgs := splitCppType(target)
		if next == "" || next == base {
			break
		}

		base, args = next, nextArgs
	}

	return base, args
}

var cppPointerWrappers = map[string]bool{
	"unique_ptr": true, "shared_ptr": true, "optional": true, "not_null": true,
	"intrusive_ptr": true, "scoped_refptr": true, "observer_ptr": true,
}

// cppBuiltinTypes are names that can never be the owner, so a receiver of such
// a type is not reported as unresolved.
var cppBuiltinTypes = map[string]bool{
	"bool": true, "char": true, "short": true, "int": true, "long": true, "float": true, "double": true,
	"void": true, "unsigned": true, "signed": true, "size_t": true, "int8_t": true, "int16_t": true,
	"int32_t": true, "int64_t": true, "uint8_t": true, "uint16_t": true, "uint32_t": true, "uint64_t": true,
}

// isExternalScope is true for a qualified name whose first segment is neither
// an indexed namespace or class nor a namespace alias anywhere in the index:
// it lives outside the indexed code (std::, godot::, a vendored library).
func (r *callResolver) isExternalScope(name string) bool {
	name = strings.TrimPrefix(name, "::")
	if strings.HasPrefix(name, "std::") {
		return true
	}

	if !strings.Contains(name, "::") {
		return false
	}

	first := firstSegment(name)
	_, aliased := r.idx.NamespaceAliases[first]

	return !r.known[first] && !aliased
}

// isQuietType is true for a type that provably is not the owner or derived
// from it: a primitive, or an external-scope name whose last segment is no
// indexed class name (so it cannot be an indexed class written through an
// unread alias or include).
func (r *callResolver) isQuietType(name string) bool {
	return cppBuiltinTypes[name] || strings.HasPrefix(name, "std::") ||
		r.isExternalScope(name) && len(r.classes[lastSegment(name)]) == 0
}

// isNamespace reports whether name denotes an indexed namespace.
func (r *callResolver) isNamespace(name string) bool {
	for _, ns := range r.namespaces[lastSegment(name)] {
		if scopesCompatible(ns, name) {
			return true
		}
	}

	return false
}

func unreadableTypeReason(what string, name string) string {
	return what + " `" + name + "` is not an indexed class (template parameter, unread alias or external type)"
}

// inheritVerdict decides a member named r.name reached through an object (or
// scope) of indexed class `class`, without virtual dispatch: a caller when the
// class inherits the owner's method, none when the class or a fully resolved
// base chain supplies a different one or none at all, unresolved when a base on
// the way is ambiguous or not indexed.
func (r *callResolver) inheritVerdict(class string, depth int) (callVerdict, string) {
	if slices.Contains(r.sub, class) {
		return verdictCaller, ""
	}

	if depth > 8 {
		return verdictUnresolved, "class `" + class + "` has a base chain deeper than the resolver follows"
	}

	for _, symbol := range r.classes[lastSegment(class)] {
		if symbol.Qualified == class {
			if verdict, reason, decided := r.usingVerdict(symbol, depth); decided {
				return verdict, reason
			}
		}
	}

	switch declares, unsure := r.declaresMethod(class); {
	case unsure:
		return verdictUnresolved, "an out-of-class `" + r.name + "` definition matches class `" + class + "` and another class of that name"
	case declares:
		return verdictNone, "" // the class's own member hides any inherited one
	}

	verdict, reason := verdictNone, ""

	for _, symbol := range r.classes[lastSegment(class)] {
		if symbol.Qualified != class {
			continue
		}

		for _, base := range symbol.Bases {
			status, qualified := r.resolveType(base, symbol.Qualified)

			switch {
			case status == typeAmbiguous:
				return verdictUnresolved, "class `" + class + "` has base `" + base + "`, which " + strings.TrimPrefix(r.ambiguousReason(base), "class name `"+base+"` ")
			case status == typeNone && r.isQuietType(base):
				continue
			case status == typeNone:
				return verdictUnresolved, "class `" + class + "` has base `" + base + "` that is not an indexed class (template parameter, unread alias or external type)"
			}

			switch v, why := r.inheritVerdict(qualified, depth+1); v {
			case verdictCaller:
				return verdictCaller, ""
			case verdictUnresolved:
				verdict, reason = v, why
			}
		}
	}

	return verdict, reason
}

// classVerdict decides a member call through a value whose static type is the
// written class name: a caller when it reaches the owner's method, unresolved
// when the type or its base chain is unreadable or virtual dispatch may reach
// the owner, none only when the type resolves to a different indexed class (or
// a quiet external type) that cannot reach it.
func (r *callResolver) classVerdict(what string, name string, ctx string) (callVerdict, string) {
	status, qualified := r.resolveType(name, ctx)

	switch {
	case status == typeAmbiguous:
		return verdictUnresolved, r.ambiguousReason(name)
	case status == typeNone && r.isQuietType(name):
		return verdictNone, ""
	case status == typeNone:
		return verdictUnresolved, unreadableTypeReason(what, name)
	case slices.Contains(r.sub, qualified):
		return verdictCaller, ""
	case r.isAncestorOfSub(qualified):
		return verdictUnresolved, "virtual dispatch via base `" + name + "`; the object's dynamic type may be the owner"
	}

	return r.inheritVerdict(qualified, 0)
}

// typeVerdict decides a receiver from its declared type text.
func (r *callResolver) typeVerdict(receiver string, typeText string, arrow bool, ctx string) (callVerdict, string) {
	if typeText == cppAmbiguousType {
		return verdictUnresolved, "receiver `" + receiver + "` is declared with several different types in the caller"
	}

	base, args := r.resolveAlias(splitCppType(typeText))

	switch {
	case base == cppAmbiguousType:
		return verdictUnresolved, "receiver `" + receiver + "` has a type alias with several targets"
	case base == "auto" || strings.HasPrefix(base, "decltype"):
		return verdictUnresolved, "receiver `" + receiver + "` is auto-typed; its type is not readable from syntax"
	case arrow && args != "" && (cppPointerWrappers[lastSegment(base)] || r.isQuietType(base)):
		// p->run() through a smart pointer, iterator or other external template
		// reaches its first template argument.
		verdict, reason := r.typeVerdict(receiver, firstTemplateArg(args), false, ctx)
		if verdict == verdictCaller && !cppPointerWrappers[lastSegment(base)] {
			return verdictUnresolved, "receiver `" + receiver + "` is a `" + base + "` whose -> may reach the owner"
		}

		return verdict, reason
	}

	return r.classVerdict("receiver type", base, ctx)
}

// callerScopeVerdict decides an unqualified or this-> call inside caller scope.
func (r *callResolver) callerScopeVerdict(scope string) (callVerdict, string) {
	if scope == "" {
		return verdictNone, "" // a free function's plain call cannot reach a member
	}

	status, qualified := r.resolveType(scope, "")

	switch {
	case status == typeAmbiguous:
		return verdictUnresolved, r.ambiguousReason(scope)
	case status == typeFound:
		verdict, reason := r.inheritVerdict(qualified, 0)
		if verdict == verdictUnresolved {
			reason = "caller " + reason
		}

		return verdict, reason
	case r.isNamespace(scope):
		return verdictNone, "" // a namespace-scope function has no members
	}

	return verdictUnresolved, unreadableTypeReason("caller scope", scope)
}

// isAncestorOfSub reports whether base is a proper base class of an owner-side
// class that declares the method, so a call through it may dispatch to the owner.
func (r *callResolver) isAncestorOfSub(base string) bool {
	for _, qualified := range r.sub {
		if r.hasAncestor(qualified, base, 0) {
			return true
		}
	}

	return false
}

func (r *callResolver) hasAncestor(class string, ancestor string, depth int) bool {
	if depth > 6 {
		return false
	}

	for _, symbol := range r.classes[lastSegment(class)] {
		if !scopesCompatible(symbol.Qualified, class) {
			continue
		}

		for _, base := range symbol.Bases {
			declares, unsure := r.declaresMethod(base)
			if scopesCompatible(base, ancestor) && (declares || unsure) || r.hasAncestor(base, ancestor, depth+1) {
				return true
			}
		}
	}

	return false
}

func firstTemplateArg(args string) string {
	depth := 0

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				return args[:i]
			}
		}
	}

	return args
}

// fieldType finds a data member's declared type in class scope or its bases.
func (r *callResolver) fieldType(scope string, field string, depth int) (string, bool) {
	if scope == "" || depth > 4 {
		return "", false
	}

	for _, class := range r.classes[lastSegment(scope)] {
		if !scopesCompatible(class.Qualified, scope) {
			continue
		}

		if typeText, ok := class.Fields[field]; ok {
			return typeText, true
		}

		for _, base := range class.Bases {
			if typeText, ok := r.fieldType(base, field, depth+1); ok {
				return typeText, true
			}
		}
	}

	return "", false
}

// classify decides what one call inside caller is relative to the owner.
// verdictNone is returned only with a proof that the call cannot reach it.
func (r *callResolver) classify(caller CppSymbol, call CppCall) (callVerdict, string) {
	switch call.Via {
	case callViaQualified:
		return r.classifyQualified(caller, call)
	case callViaDot, callViaArrow:
		if !r.isMember {
			return verdictNone, "" // a member call cannot reach a free function or constructor
		}

		return r.classifyMember(caller, call)
	}

	if spans, local := caller.Vars[cppLocalFuncPrefix+call.Name]; local && localInScope(spans, call.Line) {
		return verdictNone, "" // a local variable (lambda) in scope, called like a function
	}

	switch {
	case r.isType:
		return r.ownerTypeVerdict(call.Name, caller.Scope)
	case r.isMember:
		return r.callerScopeVerdict(caller.Scope)
	}

	if caller.Scope != "" {
		switch declares, unsure := r.declaresMethod(caller.Scope); {
		case unsure:
			return verdictUnresolved, "caller class `" + caller.Scope + "` may declare its own `" + call.Name + "`"
		case declares:
			return verdictNone, "" // the caller's class member of that name hides the free function
		}
	}

	return verdictCaller, ""
}

// ownerTypeVerdict decides a constructor call written as name (from ctx).
func (r *callResolver) ownerTypeVerdict(name string, ctx string) (callVerdict, string) {
	status, qualified := r.resolveType(name, ctx)

	switch {
	case status == typeAmbiguous:
		return verdictUnresolved, r.ambiguousReason(name)
	case status == typeFound && scopesCompatible(qualified, r.owner):
		return verdictCaller, ""
	case status == typeFound, r.isQuietType(name):
		return verdictNone, ""
	}

	return verdictUnresolved, unreadableTypeReason("constructor", name)
}

func (r *callResolver) classifyQualified(caller CppSymbol, call CppCall) (callVerdict, string) {
	if call.Qualifier == "?" {
		return verdictUnresolved, "qualifier is a template-id the syntax index cannot read"
	}

	qualifier, _ := r.resolveAlias(call.Qualifier, "")

	switch {
	case r.isType:
		return r.ownerTypeVerdict(joinCppScope(qualifier, call.Name), caller.Scope)
	case r.isMember:
		if qualifier == "" || r.isNamespace(qualifier) || r.isExternalScope(qualifier) && len(r.classes[lastSegment(qualifier)]) == 0 {
			return verdictNone, "" // ns::name() names a free function, not a member
		}

		// X::run() is a static (non-virtual) call: no dispatch through X.
		status, qualified := r.resolveType(qualifier, caller.Scope)

		switch status {
		case typeAmbiguous:
			return verdictUnresolved, r.ambiguousReason(qualifier)
		case typeNone:
			return verdictUnresolved, unreadableTypeReason("qualifier", qualifier)
		}

		return r.inheritVerdict(qualified, 0)
	}

	switch {
	case qualifier == "" && r.scope == "", qualifier != "" && r.scope != "" && scopesCompatible(qualifier, r.scope):
		return verdictCaller, ""
	case qualifier == "", r.isNamespace(qualifier), r.isExternalScope(qualifier):
		return verdictNone, "" // a different indexed or external namespace
	}

	if status, _ := r.resolveType(qualifier, caller.Scope); status == typeFound {
		return verdictNone, "" // Class::name() is a member, not the free function
	}

	return verdictUnresolved, "qualifier `" + qualifier + "` is neither an indexed namespace nor an indexed class"
}

func (r *callResolver) classifyMember(caller CppSymbol, call CppCall) (callVerdict, string) {
	receiver := call.Receiver
	if receiver == "" {
		return verdictUnresolved, "receiver is a call result, index or chained member; its type is not readable from syntax"
	}

	if receiver == "this" {
		return r.callerScopeVerdict(caller.Scope)
	}

	outer, ok := caller.Vars[receiver]
	if !ok {
		outer, ok = r.fieldType(caller.Scope, receiver, 0)
	}

	typeText := outer

	// Several declarations of the receiver: the innermost covering one wins,
	// unless the call shares a line with that declaration's start or end and
	// another declaration in scope has a different type (no proof which applies).
	if decls := scopedVarDecls(caller.Vars[cppScopedVarPrefix+receiver], call.Line); len(decls) > 0 {
		alternative, hasAlternative := outer, ok
		if len(decls) > 1 {
			alternative, hasAlternative = decls[1].typeText, true
		}

		if decls[0].boundary && hasAlternative && alternative != decls[0].typeText {
			return verdictUnresolved, "receiver `" + receiver + "` has several declarations with different types and the call shares a line with a scope boundary; which one applies is not readable"
		}

		typeText, ok = decls[0].typeText, true
	}

	if !ok {
		return verdictUnresolved, "receiver `" + receiver + "` is not a parameter, local or field of the caller's class; its type is unknown"
	}

	return r.typeVerdict(receiver, typeText, call.Via == callViaArrow, caller.Scope)
}

// CallsInto returns the call sites that resolve to the resolved owner's method
// (or function, or constructor): member calls on an object whose declared type
// is the owner or derived from it, qualified calls, and unqualified calls
// inside the owner's own members. Calls whose receiver type cannot be read are
// reported as Unresolved, never as callers. A non-empty pathFilter restricts
// call sites to paths under that prefix; limit truncates each list (0 = all).
func (idx *CppIndex) CallsInto(res CppResolution, pathFilter string, limit int) CppCallers {
	if idx == nil || res.Name == "" {
		return CppCallers{}
	}

	resolver := newCallResolver(idx, res)
	callerSites := make(map[callSite]bool)
	unresolvedSites := make(map[callSite]bool)

	var out CppCallers

	for _, file := range idx.Files {
		if pathFilter != "" && !strings.HasPrefix(file.Path, pathFilter) {
			continue
		}

		for _, symbol := range file.Symbols {
			for _, call := range symbol.Calls {
				if call.Name != res.Name {
					continue
				}

				site := callSite{file.Path, call.Line}

				verdict, reason := resolver.classify(symbol, call)

				switch {
				case verdict == verdictCaller && !callerSites[site]:
					callerSites[site] = true

					out.Callers = append(out.Callers, CppCallMatch{Caller: symbolMatch(file.Path, symbol), CalleeName: call.Name, Line: call.Line})
				case verdict == verdictUnresolved && !unresolvedSites[site]:
					unresolvedSites[site] = true

					out.Unresolved = append(out.Unresolved, CppUnresolvedCall{Caller: symbolMatch(file.Path, symbol), CalleeName: call.Name, Line: call.Line, Reason: reason})
				}
			}
		}
	}

	slices.SortFunc(out.Callers, func(a, b CppCallMatch) int {
		return cmp.Or(cmp.Compare(a.Caller.Path, b.Caller.Path), cmp.Compare(a.Line, b.Line))
	})
	slices.SortFunc(out.Unresolved, func(a, b CppUnresolvedCall) int {
		return cmp.Or(cmp.Compare(a.Caller.Path, b.Caller.Path), cmp.Compare(a.Line, b.Line))
	})

	out.Total, out.UnresolvedTotal = len(out.Callers), len(out.Unresolved)

	if limit > 0 {
		out.Callers = out.Callers[:min(limit, len(out.Callers))]
		out.Unresolved = out.Unresolved[:min(limit, len(out.Unresolved))]
	}

	return out
}
