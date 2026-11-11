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
	r := &callResolver{idx: idx, name: res.Name, owner: res.Owner, classes: make(map[string][]*CppSymbol)}

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
				if r.inSub(base) {
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

func (r *callResolver) inSub(name string) bool {
	for _, qualified := range r.sub {
		if scopesCompatible(name, qualified) {
			return true
		}
	}

	return false
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

// typeIsOwner reports whether an object of declared type typeText dispatches
// to the owner's method. The second result is true when the type is auto (not
// readable from syntax).
func (r *callResolver) typeIsOwner(typeText string, arrow bool) (match bool, auto bool) {
	base, args := splitCppType(typeText)
	base, args = r.resolveAlias(base, args)

	if arrow && cppPointerWrappers[lastSegment(base)] && args != "" {
		inner := args
		depth := 0

		for i := 0; i < len(args); i++ {
			switch args[i] {
			case '<':
				depth++
			case '>':
				depth--
			case ',':
				if depth == 0 {
					inner = args[:i]
					i = len(args)
				}
			}
		}

		return r.typeIsOwner(inner, false)
	}

	if base == "auto" || strings.HasPrefix(base, "decltype") {
		return false, true
	}

	return r.inSub(base), false
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
		return r.classifyQualified(call)
	case callViaDot, callViaArrow:
		if !r.isMember {
			return verdictNone, ""
		}

		return r.classifyMember(caller, call)
	}

	switch {
	case r.isType:
		return verdictCaller, ""
	case r.isMember:
		if caller.Scope != "" && r.inSub(caller.Scope) {
			return verdictCaller, ""
		}

		return verdictNone, ""
	}

	if caller.Scope != "" && r.declaresMethod(caller.Scope) {
		return verdictNone, ""
	}

	return verdictCaller, ""
}

func (r *callResolver) classifyQualified(call CppCall) (callVerdict, string) {
	if call.Qualifier == "?" {
		return verdictUnresolved, "qualifier is a template-id the syntax index cannot read"
	}

	qualifier, _ := r.resolveAlias(call.Qualifier, "")

	switch {
	case r.isType:
		if scopesCompatible(qualifier+"::"+call.Name, r.owner) {
			return verdictCaller, ""
		}
	case r.isMember:
		if r.inSub(qualifier) {
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
		if caller.Scope != "" && r.inSub(caller.Scope) {
			return verdictCaller, ""
		}

		return verdictNone, ""
	}

	typeText, ok := caller.Vars[receiver]
	if !ok {
		typeText, ok = r.fieldType(caller.Scope, receiver, 0)
	}

	if !ok {
		return verdictUnresolved, "receiver `" + receiver + "` is not a parameter, local or field of the caller's class; its type is unknown"
	}

	match, auto := r.typeIsOwner(typeText, arrow)
	if auto {
		return verdictUnresolved, "receiver `" + receiver + "` is auto-typed; its type is not readable from syntax"
	}

	if match {
		return verdictCaller, ""
	}

	return verdictNone, ""
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
