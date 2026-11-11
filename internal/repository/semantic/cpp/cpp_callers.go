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
	known     map[string]bool         // first namespace segment of every indexed symbol
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
	r := &callResolver{idx: idx, name: res.Name, owner: res.Owner, classes: make(map[string][]*CppSymbol), known: make(map[string]bool)}

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
			case symbol.Kind == "class" || symbol.Kind == "struct" || symbol.Kind == "union":
				r.classes[symbol.Name] = append(r.classes[symbol.Name], symbol)
				all = append(all, symbol)
			case symbol.Name == r.name && symbol.Scope != "" && symbol.Kind == "method":
				r.methodsOf = append(r.methodsOf, symbol.Scope)
			}
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
			if slices.Contains(r.sub, class.Qualified) || r.declaresMethod(class.Qualified) {
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

func (r *callResolver) declaresMethod(class string) bool {
	for _, scope := range r.methodsOf {
		if scopesCompatible(scope, class) {
			return true
		}
	}

	return false
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
	name = strings.TrimPrefix(name, "::")

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

// isBuiltinType is true for primitives and for names in a namespace that has no
// indexed symbol (std::, a vendored library), which cannot be the owner.
func (r *callResolver) isBuiltinType(base string) bool {
	if cppBuiltinTypes[base] || strings.HasPrefix(base, "std::") {
		return true
	}

	return strings.Contains(base, "::") && !r.known[firstSegment(base)]
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
			if scopesCompatible(base, ancestor) && r.declaresMethod(base) || r.hasAncestor(base, ancestor, depth+1) {
				return true
			}
		}
	}

	return false
}

// typeVerdict says what a declared receiver type means for the owner: a caller
// when it dispatches to the owner's method, none when it provably does not, and
// unresolved (with the reason) when the type is not readable or not indexed.
func (r *callResolver) typeVerdict(receiver string, typeText string, arrow bool, ctx string) (callVerdict, string) {
	if typeText == cppAmbiguousType {
		return verdictUnresolved, "receiver `" + receiver + "` is declared with several different types in the caller"
	}

	base, args := splitCppType(typeText)

	if verdict, reason, decided := r.subVerdict(base, ctx); decided {
		return verdict, reason
	}

	base, args = r.resolveAlias(base, args)

	if verdict, reason, decided := r.subVerdict(base, ctx); decided {
		return verdict, reason
	}

	if arrow && cppPointerWrappers[lastSegment(base)] && args != "" {
		return r.typeVerdict(receiver, firstTemplateArg(args), false, ctx)
	}

	switch {
	case base == "auto" || strings.HasPrefix(base, "decltype"):
		return verdictUnresolved, "receiver `" + receiver + "` is auto-typed; its type is not readable from syntax"
	case r.isAncestorOfSub(base):
		return verdictUnresolved, "virtual dispatch via base `" + base + "`; the object's dynamic type may be the owner"
	case len(r.classes[lastSegment(base)]) > 0, r.isBuiltinType(base):
		return verdictNone, ""
	}

	return verdictUnresolved, "receiver type `" + base + "` is not an indexed class (template parameter, external type or alias)"
}

// subVerdict decides a class name against the owner's hierarchy: a caller when
// it resolves into it, unresolved when the name is ambiguous, undecided when the
// name is not an indexed class or resolves outside the hierarchy.
func (r *callResolver) subVerdict(name string, ctx string) (callVerdict, string, bool) {
	status, qualified := r.resolveType(name, ctx)

	switch {
	case status == typeAmbiguous:
		return verdictUnresolved, r.ambiguousReason(name), true
	case status == typeFound && slices.Contains(r.sub, qualified):
		return verdictCaller, "", true
	}

	return verdictNone, "", false
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

// unindexedBase returns a base class of scope (or of its bases) that is not an
// indexed class, such as a template parameter in CRTP; an unqualified or this
// call in such a class may reach an inherited member the index cannot see.
func (r *callResolver) unindexedBase(scope string, depth int) string {
	if scope == "" || depth > 4 {
		return ""
	}

	for _, class := range r.classes[lastSegment(scope)] {
		if !scopesCompatible(class.Qualified, scope) {
			continue
		}

		for _, base := range class.Bases {
			if len(r.classes[lastSegment(base)]) == 0 && !r.isBuiltinType(base) {
				return base
			}

			if inner := r.unindexedBase(base, depth+1); inner != "" {
				return inner
			}
		}
	}

	return ""
}

// callerScopeVerdict decides an unqualified or this-> call inside a member of
// scope: a caller when scope is in the owner's hierarchy, unresolved when an
// unindexed base could supply the member, otherwise not a call to the owner.
func (r *callResolver) callerScopeVerdict(scope string) (callVerdict, string) {
	if scope == "" {
		return verdictNone, ""
	}

	if verdict, reason, decided := r.subVerdict(scope, ""); decided {
		return verdict, reason
	}

	if base := r.unindexedBase(scope, 0); base != "" {
		return verdictUnresolved, "caller class `" + scope + "` has base `" + base + "` that is not an indexed class (template parameter or external type)"
	}

	return verdictNone, ""
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
func (r *callResolver) classify(caller CppSymbol, call CppCall) (callVerdict, string) {
	switch call.Via {
	case callViaQualified:
		return r.classifyQualified(caller, call)
	case callViaDot, callViaArrow:
		if !r.isMember {
			return verdictNone, ""
		}

		return r.classifyMember(caller, call)
	}

	if _, local := caller.Vars[cppLocalFuncPrefix+call.Name]; local {
		return verdictNone, "" // a local variable (lambda) called like a function
	}

	switch {
	case r.isType:
		return verdictCaller, ""
	case r.isMember:
		return r.callerScopeVerdict(caller.Scope)
	}

	if caller.Scope != "" && r.declaresMethod(caller.Scope) {
		return verdictNone, ""
	}

	return verdictCaller, ""
}

func (r *callResolver) classifyQualified(caller CppSymbol, call CppCall) (callVerdict, string) {
	if call.Qualifier == "?" {
		return verdictUnresolved, "qualifier is a template-id the syntax index cannot read"
	}

	qualifier, _ := r.resolveAlias(call.Qualifier, "")

	if r.isMember {
		if verdict, reason, decided := r.subVerdict(call.Qualifier, caller.Scope); decided {
			return verdict, reason
		}

		if verdict, reason, decided := r.subVerdict(qualifier, caller.Scope); decided {
			return verdict, reason
		}

		return verdictNone, ""
	}

	switch {
	case r.isType:
		if scopesCompatible(qualifier+"::"+call.Name, r.owner) {
			return verdictCaller, ""
		}
	case qualifier == "" && r.scope == "", qualifier != "" && r.scope != "" && scopesCompatible(qualifier, r.scope):
		return verdictCaller, ""
	}

	return verdictNone, ""
}

func (r *callResolver) classifyMember(caller CppSymbol, call CppCall) (callVerdict, string) {
	receiver := call.Receiver
	if receiver == "" {
		return verdictUnresolved, "receiver is a call result, index or chained member; its type is not readable from syntax"
	}

	arrow := call.Via == callViaArrow

	if receiver == "this" {
		return r.callerScopeVerdict(caller.Scope)
	}

	typeText, ok := caller.Vars[receiver]
	if !ok {
		typeText, ok = r.fieldType(caller.Scope, receiver, 0)
	}

	if !ok {
		return verdictUnresolved, "receiver `" + receiver + "` is not a parameter, local or field of the caller's class; its type is unknown"
	}

	return r.typeVerdict(receiver, typeText, arrow, caller.Scope)
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
