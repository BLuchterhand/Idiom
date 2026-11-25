package comprehension

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Lexical retrieval: the text of every indexed file (plus its path) is split
// into identifier-aware terms and scored with Okapi BM25. This is the
// candidate source for context_impact; the symbol graph supplies symbols,
// tests and relationships around those files, not the files themselves.
// The same tokenizer and scoring run in the localization eval baselines
// (internal/evidence/eval), so a measured baseline is the production ranker.

const (
	// BM25 constants of Robertson and Zaragoza (2009), the published defaults.
	bm25K1 = 1.2
	bm25B  = 0.75

	// LexicalMaxFileBytes skips generated or vendored blobs that would
	// dominate length normalization; their path is still indexed.
	LexicalMaxFileBytes = 1 << 20

	// lexicalTestWeight multiplies the score of test files: a task names the
	// code it changes far more often than the tests that cover it.
	lexicalTestWeight = 0.5

	minLexicalTermLen = 2
	pluralMinLen      = 3
)

// lexicalStopwords are English function words plus ledger boilerplate that
// names no code.
var lexicalStopwords = map[string]struct{}{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be by for from has have in into is it its of on or
		that the this to was were will with not no so but if then than when while what which who how why
		all any each every one two per via vs only also more most less very just still now new same own
		do does did done can could should would may might must our we you they them their there here
		designer playtest main red green fix fixes`) {
		lexicalStopwords[w] = struct{}{}
	}
}

// lexicalTestPathRE marks test files by path shape, language-neutral.
var lexicalTestPathRE = regexp.MustCompile(`(^|/)tests?/|_test\.|(^|/)test_`)

// IsLexicalTestPath reports whether path names a test file for score demotion.
func IsLexicalTestPath(path string) bool {
	return lexicalTestPathRE.MatchString(path)
}

// LexicalTokens splits text into lowercase index terms: identifiers are split
// on snake_case, camelCase, `::` and letter/digit boundaries, the joined
// compound is kept too (world_gen also matches worldgen), single characters,
// pure numbers and stopwords are dropped, and a plural s is stripped.
func LexicalTokens(text string) []string {
	var out []string

	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
		parts := splitIdentifier(word)
		for _, p := range parts {
			out = appendLexicalTerm(out, p)
		}

		if len(parts) > 1 {
			out = appendLexicalTerm(out, strings.Join(parts, ""))
		}
	}

	return out
}

func appendLexicalTerm(out []string, t string) []string {
	if len(t) < minLexicalTermLen {
		return out
	}

	if _, err := strconv.Atoi(t); err == nil {
		return out
	}

	if _, stop := lexicalStopwords[t]; stop {
		return out
	}

	if len(t) > pluralMinLen && strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss") {
		t = t[:len(t)-1]
	}

	return append(out, t)
}

func splitIdentifier(word string) []string {
	var parts []string

	for _, piece := range strings.Split(word, "_") {
		runes := []rune(piece)
		start := 0

		for i := 1; i < len(runes); i++ {
			prev, cur := runes[i-1], runes[i]
			next := rune(0)

			if i+1 < len(runes) {
				next = runes[i+1]
			}

			boundary := (unicode.IsLower(prev) && unicode.IsUpper(cur)) ||
				(unicode.IsUpper(prev) && unicode.IsUpper(cur) && unicode.IsLower(next)) ||
				(unicode.IsLetter(prev) != unicode.IsLetter(cur))
			if boundary {
				parts = append(parts, strings.ToLower(string(runes[start:i])))
				start = i
			}
		}

		if start < len(runes) {
			parts = append(parts, strings.ToLower(string(runes[start:])))
		}
	}

	return parts
}

// LexicalDocument is one file's index form: the term frequencies of its path
// and text, and its token length.
type LexicalDocument struct {
	Terms  map[string]int
	Length int
}

// NewLexicalDocument tokenizes a file's path and, when text is not nil, its
// content. The path counts as part of the text so a file named for the task
// scores on its name.
func NewLexicalDocument(path string, text []byte) LexicalDocument {
	tokens := LexicalTokens(path)
	if len(text) > 0 && len(text) <= LexicalMaxFileBytes {
		tokens = append(tokens, LexicalTokens(string(text))...)
	}

	doc := LexicalDocument{Terms: make(map[string]int, len(tokens)/2+1), Length: len(tokens)}
	for _, t := range tokens {
		doc.Terms[t]++
	}

	return doc
}

// BM25IDF is the inverse document frequency with the +1 floor, so no term
// scores negative.
func BM25IDF(docs, docFreq int) float64 {
	return math.Log(1 + (float64(docs)-float64(docFreq)+0.5)/(float64(docFreq)+0.5))
}

// BM25TermScore is the saturating, length-normalized term-frequency factor;
// multiply by BM25IDF for the term's contribution.
func BM25TermScore(termFreq, docLen int, avgLen float64) float64 {
	c := float64(termFreq)

	return c * (bm25K1 + 1) / (c + bm25K1*(1-bm25B+bm25B*float64(docLen)/avgLen))
}

// BM25AvgLength is the mean document length, floored at 1.
func BM25AvgLength(totalLen, docs int) float64 {
	if docs == 0 {
		return 1
	}

	return math.Max(float64(totalLen)/float64(docs), 1)
}

// UniqueLexicalTerms keeps the first occurrence of each term, in order.
func UniqueLexicalTerms(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}

	return out
}
