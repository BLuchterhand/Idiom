package languagetools

import (
	"fmt"
	"regexp"
	"strings"
)

// funcCallShapeRE matches a function-call shape: parens with at least
// one non-whitespace character inside. Empty "()" is intentionally
// excluded so Java-style method declarations like "public void run()"
// pass through; only "name(arg)" / "static_cast<int>(value)" are
// flagged as expression-shaped, since read_symbol cannot resolve a
// call.
var funcCallShapeRE = regexp.MustCompile(`\(\s*[^)\s][^)]*\)`)

// validateSymbolPattern rejects patterns that are obviously prose,
// function calls, or arithmetic/boolean expressions before they hit
// the per-language indexer. The model loses minutes looping on
// "no symbols matched" when it asks read_symbol for a literal
// expression — this returns a directional error pointing at
// search_content / ast_grep so the model can pivot on the next turn.
//
// The validator must accept legitimate "declaration-shaped" hints
// (e.g. "struct EntityManager", "type Runner struct", "class Engine",
// "pub struct Worker", "export class Shouter",
// "template <typename T> class EntityBox", "public void run()",
// "Helper#run") since the indexers match against the symbol name
// embedded in those phrases. So the rules can't simply forbid spaces
// or parens — they target the shapes that uniquely indicate a call,
// a comparison, or prose.
//
// Returns nil for empty patterns; the caller's existing
// "pattern is required" check owns that error message.
func validateSymbolPattern(tool, pattern string) error {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return nil
	}

	// C++ operator overloads (operator==, operator(), operator<<,
	// operator new, operator delete) carry characters that the
	// expression checks below would reject. They're real symbols.
	if strings.HasPrefix(p, "operator") {
		return nil
	}

	// Function-call shapes: parens with content inside. A real
	// declaration uses "()" with nothing between; a call carries
	// arguments.
	if funcCallShapeRE.MatchString(p) {
		return symbolPatternRejection(tool, p)
	}

	// Boolean / comparison / arithmetic operators surrounded by
	// whitespace. Template syntax uses tight <Foo>, never " < ".
	// Declarations never include " && ", " == ", " > ", etc.
	for _, op := range []string{" && ", " || ", " == ", " != ", " >= ", " <= ", " > ", " < ", " + ", " - ", " / ", " % "} {
		if strings.Contains(p, op) {
			return symbolPatternRejection(tool, p)
		}
	}

	// Prose: long token sequences. The longest legitimate declaration
	// we accept is "template <typename T> class EntityBox" at five
	// whitespace tokens. Anything beyond six is prose.
	if len(strings.Fields(p)) > 6 {
		return symbolPatternRejection(tool, p)
	}

	return nil
}

func ValidateSymbolPattern(tool, pattern string) error {
	return validateSymbolPattern(tool, pattern)
}

func symbolPatternRejection(tool, pattern string) error {
	return fmt.Errorf(
		"%s: pattern %q does not look like a symbol identifier or declaration shape (it contains a function call, a comparison/boolean expression, or runs longer than a declaration). "+
			"Literal text or expressions need exact-text search with the literal as the pattern. "+
			"Structural code patterns (call shapes, expression shapes) need an AST-pattern capability. "+
			"read_symbol expects identifiers like 'MyClass' or 'MyClass::method', or short declaration hints like 'struct Foo', 'class Engine', 'type Runner struct'",
		tool, pattern,
	)
}
