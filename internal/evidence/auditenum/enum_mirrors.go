package auditenum

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Run finds the cross-file mirror sites that break
// when an enum changes. Generalizes Colony's enum_mirror_audit.sh:
// recursively walks the workspace, lists files referencing the
// named symbol, and per-file scans for high-signal mirror patterns
// (count assertions, name tables, switch cases).
//
// Pattern set + extension filter come from a named --language profile
// (cpp default; go, rust, ts, any). --pattern adds more regexes (with
// {ENUM} substitution) and --ext narrows the file set — both stack on
// top of the profile so the operator can stay on a profile while
// covering project-specific conventions.
//
// Exit codes: 0 successful audit (regardless of whether matches
// were found), 1 IO error / no references found, 2 flag-parse error.
func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom audit enum-mirrors", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		enumName   string
		root       string
		language   string
		patternArg multiString
		extArg     multiString
	)

	fs.StringVar(&enumName, "enum", "", "enum or symbol name to audit (required)")
	fs.StringVar(&root, "root", "", "search root (defaults to cwd)")
	fs.StringVar(&language, "language", "cpp", "named pattern/extension profile: cpp | go | rust | ts | any (any disables built-in patterns)")
	fs.Var(&patternArg, "pattern", "extra mirror-pattern regex (repeatable). Use {ENUM} as a placeholder for the symbol name.")
	fs.Var(&extArg, "ext", "file extension to consider (repeatable; overrides the profile's default extensions)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	enumName = strings.TrimSpace(enumName)
	if enumName == "" {
		fmt.Fprintln(stderr, "audit enum-mirrors: --enum is required")

		return 2
	}

	if strings.TrimSpace(root) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		root = cwd
	}

	// Stat the search root explicitly so a typo'd path produces a
	// clear "not a directory" diagnostic rather than the vague
	// "no references found" the empty-walk path used to emit.
	if info, err := os.Stat(root); err != nil {
		fmt.Fprintf(stderr, "audit enum-mirrors: search root %q: %v\n", root, err)

		return 1
	} else if !info.IsDir() {
		fmt.Fprintf(stderr, "audit enum-mirrors: search root %q is not a directory\n", root)

		return 1
	}

	profile, ok := enumMirrorProfileFor(language)
	if !ok {
		fmt.Fprintf(stderr, "audit enum-mirrors: unknown --language %q (try: cpp | go | rust | ts | any)\n", language)

		return 2
	}

	exts := profile.extensions
	if len(extArg) > 0 {
		exts = make(map[string]bool, len(extArg))
		for _, e := range extArg {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}

			if !strings.HasPrefix(e, ".") {
				e = "." + e
			}

			exts[strings.ToLower(e)] = true
		}
	}

	patterns := profile.patternsFor(enumName)
	for _, p := range patternArg {
		filled := strings.ReplaceAll(p, "{ENUM}", regexp.QuoteMeta(enumName))

		re, err := regexp.Compile(filled)
		if err != nil {
			fmt.Fprintf(stderr, "audit enum-mirrors: invalid --pattern %q: %v\n", p, err)

			return 2
		}

		patterns = append(patterns, re)
	}

	fmt.Fprintf(stdout, "═══ Enum mirror audit: %s ═══\n\n", enumName)

	files, err := filesReferencingSymbol(root, enumName, exts)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if len(files) == 0 {
		fmt.Fprintf(stdout, "[enum_mirror_audit] no references found for %s\n", enumName)
		fmt.Fprintln(stdout, "[enum_mirror_audit] verify the spelling and that the search root contains the project source.")

		return 1
	}

	rel := relativizeFiles(files, root)
	fmt.Fprintf(stdout, "Step 1 — found %d file(s) referencing %s:\n\n", len(rel), enumName)

	for _, f := range rel {
		fmt.Fprintln(stdout, "  "+f)
	}

	fmt.Fprintln(stdout)

	fmt.Fprintln(stdout, "Step 2 — scanning each file for mirror-site invariants:")
	fmt.Fprintln(stdout)

	hadHits := scanMirrorPatterns(stdout, stderr, files, patterns)
	if !hadHits {
		fmt.Fprintln(stdout, "  (no high-signal mirror patterns found — review every referenced file by hand)")
		fmt.Fprintln(stdout)
	}

	fmt.Fprintln(stdout, "Step 3 — Files-To-Modify candidate list (review and trim before pasting into brief):")
	fmt.Fprintln(stdout)

	for _, f := range rel {
		fmt.Fprintf(stdout, "| `%s` | <action> |\n", f)
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "[enum_mirror_audit] Done. Files with low reference counts (≤3) are the highest")
	fmt.Fprintln(stdout, "[enum_mirror_audit] risk for missed mirror sites — examine those first.")

	return 0
}

// multiString implements flag.Value for repeatable string flags.
// Used by --pattern and --ext below.
type multiString []string

func (m *multiString) String() string     { return strings.Join(*m, ",") }
func (m *multiString) Set(v string) error { *m = append(*m, v); return nil }

// enumMirrorProfile pairs a default extension set with the high-
// signal mirror-pattern templates that fire for that language's
// idioms. Profiles are static — operators get richer patterns by
// composing --language with one or more --pattern overrides, not
// by editing the canonical set.
type enumMirrorProfile struct {
	extensions map[string]bool
	templates  []string // {ENUM} placeholder is substituted at runtime
}

// patternsFor compiles the profile's templates against a concrete
// enum name. Templates without {ENUM} compile as-is so per-language
// generic patterns (like sizeof asserts) still fire.
func (p enumMirrorProfile) patternsFor(enum string) []*regexp.Regexp {
	q := regexp.QuoteMeta(enum)
	out := make([]*regexp.Regexp, 0, len(p.templates))

	for _, tmpl := range p.templates {
		filled := strings.ReplaceAll(tmpl, "{ENUM}", q)
		// MustCompile is safe here: the templates are author-supplied
		// constants reviewed at compile time, never user input.
		out = append(out, regexp.MustCompile(filled))
	}

	return out
}

// enumMirrorProfileFor resolves the named language profile. The
// "any" profile drops the template set entirely so the operator
// can rely solely on --pattern overrides without inheriting
// cpp-shaped false positives.
func enumMirrorProfileFor(language string) (enumMirrorProfile, bool) {
	key := strings.ToLower(strings.TrimSpace(language))
	if key == "" {
		key = "cpp"
	}

	prof, ok := enumMirrorProfiles[key]

	return prof, ok
}

var enumMirrorProfiles = map[string]enumMirrorProfile{
	"cpp": {
		extensions: extensionSet(".c", ".cc", ".cxx", ".cpp", ".h", ".hh", ".hpp", ".hxx", ".gd", ".tscn"),
		// Note: the two `static_assert.*sizeof.*_NAMES` patterns
		// don't reference {ENUM}. Faithful to Colony's original
		// script — they catch ANY name-table count assertion in a
		// file that already passed the symbol grep, even if the
		// assertion belongs to a different enum family. Trade
		// precision for recall: false positives are easy to ignore;
		// missed mirrors are not.
		templates: []string{
			`static_assert.*{ENUM}::COUNT`,
			`static_assert.*sizeof.*_NAMES`,
			`static_assert.*std::size.*_NAMES`,
			`{ENUM}_NAMES\[\]`,
			`{ENUM}_DESCRIPTORS`,
			`case {ENUM}::`,
		},
	},
	"go": {
		extensions: extensionSet(".go"),
		templates: []string{
			// stringer-generated names array
			`_{ENUM}_name`,
			`_{ENUM}_index`,
			// type switches and value tables
			`case {ENUM}\(.+\)`,
			`case {ENUM}\.`,
			`\[{ENUM}\][^=]*=`,
		},
	},
	"rust": {
		extensions: extensionSet(".rs"),
		templates: []string{
			// match arms
			`{ENUM}::[A-Z][A-Za-z0-9_]*\s*=>`,
			// const arrays of variants
			`\[{ENUM};`,
			// from_str / Display impls (best-effort)
			`impl.*for {ENUM}`,
		},
	},
	"ts": {
		extensions: extensionSet(".ts", ".tsx", ".js", ".jsx"),
		templates: []string{
			// switch on enum value
			`case {ENUM}\.`,
			// name maps keyed by enum value
			`{ENUM}\.[A-Z][A-Za-z0-9_]*\s*\]`,
			// `as const` tables
			`\[{ENUM}\.[A-Z]`,
		},
	},
	"any": {
		// Common code extensions; the operator drives via --pattern.
		extensions: extensionSet(".c", ".cc", ".cxx", ".cpp", ".h", ".hh", ".hpp", ".hxx",
			".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".java", ".kt", ".gd", ".tscn"),
		templates: nil,
	},
}

// extensionSet builds a map[string]bool from a list of extensions.
// Centralizing the helper keeps the profiles table readable and
// rules out typos (e.g. ".cpp" vs "cpp" — every entry must start
// with ".") at construction time.
func extensionSet(exts ...string) map[string]bool {
	out := make(map[string]bool, len(exts))
	for _, e := range exts {
		if !strings.HasPrefix(e, ".") {
			panic("enumMirrorProfile: extension must start with '.': " + e)
		}

		out[strings.ToLower(e)] = true
	}

	return out
}

// filesReferencingSymbol walks root and returns every file whose
// extension is in exts and whose body contains symbol as a token-
// bounded substring. Token-bounded (rather than substring) search
// avoids hits on `MyEnumOther` when the operator wanted `MyEnum`.
func filesReferencingSymbol(root, symbol string, exts map[string]bool) ([]string, error) {
	tokenBoundary := regexp.MustCompile(`\b` + regexp.QuoteMeta(symbol) + `\b`)

	var matches []string

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			// EACCES on individual subtrees shouldn't kill the walk;
			// log via stderr would require a writer threaded through.
			// Skipping is safe — the operator sees no false negatives
			// (they get a smaller file list, not an audit failure).
			return nil
		}

		if info.IsDir() {
			if shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}

			return nil
		}

		if !exts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		if tokenBoundary.Match(body) {
			matches = append(matches, path)
		}

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("audit enum-mirrors: walk %s: %w", root, walkErr)
	}

	slices.Sort(matches)

	return matches, nil
}

// shouldSkipDir keeps the walker out of build artifact directories,
// VCS metadata, and dependency caches. Without this the audit would
// scan node_modules / target / build / .git on big repos and burn
// minutes for zero useful signal.
func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", ".idea", ".vscode",
		"node_modules", "vendor", "target", "build", "dist",
		".cache", "__pycache__", ".pytest_cache", ".mypy_cache",
		".idiom":
		return true
	}

	return false
}

// scanMirrorPatterns prints any matching lines per file, returning
// true when at least one match landed. Per-file output mirrors the
// Colony script's `── path ──` separator + indented hit lines so
// existing operator habits transfer. Scanner errors (e.g. a line
// longer than the 1MB cap) are logged to stderr so the operator
// knows the report may be partial.
func scanMirrorPatterns(stdout io.Writer, stderr io.Writer, files []string, patterns []*regexp.Regexp) bool {
	hadAny := false

	for _, f := range files {
		hits, err := scanFileForPatterns(f, patterns)
		if err != nil {
			fmt.Fprintf(stderr, "audit enum-mirrors: %s: %v (results may be partial)\n", f, err)
		}

		if len(hits) == 0 {
			continue
		}

		fmt.Fprintf(stdout, "  ── %s ──\n", f)

		for _, line := range hits {
			fmt.Fprintln(stdout, "    "+line)
		}

		fmt.Fprintln(stdout)

		hadAny = true
	}

	return hadAny
}

// scanFileForPatterns returns one "lineNumber:lineText" string per
// matching line, in line order. Multiple patterns matching the same
// line collapse into a single entry so the output doesn't repeat.
// Returns the partial hits + a non-nil error when the file scan
// stopped early (e.g. line longer than 1MB) so the caller can warn
// the operator that the report is incomplete for that file.
func scanFileForPatterns(path string, patterns []*regexp.Regexp) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hits []string

	scanner := bufio.NewScanner(f)
	// 10MB cap covers vendored / minified content that the dir-skip
	// list might not catch (e.g. a generated single-line file that
	// landed in src/). Anything bigger than this is probably not
	// human-authored code worth scanning anyway, and the caller
	// surfaces the scanner error so the operator knows.
	scanner.Buffer(make([]byte, 0, 1024), 10*1024*1024)

	lineno := 0
	for scanner.Scan() {
		lineno++

		line := scanner.Text()

		for _, re := range patterns {
			if re.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%d: %s", lineno, strings.TrimSpace(line)))
				break
			}
		}
	}

	return hits, scanner.Err()
}

// relativizeFiles strips root from each path so output is readable.
// Paths outside root (shouldn't happen for a well-formed walk) pass
// through unchanged.
func relativizeFiles(files []string, root string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			out = append(out, f)
			continue
		}

		out = append(out, rel)
	}

	return out
}
