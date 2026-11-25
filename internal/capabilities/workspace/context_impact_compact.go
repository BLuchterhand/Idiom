package workspace

import (
	"fmt"
	"strings"

	"idiom/internal/repository/comprehension"
)

const (
	contextFormatCompact = "compact"
	contextFormatJSON    = "json"
	// compactDefaultFiles matches the pack's default candidate limit.
	compactDefaultFiles = 8
	compactMaxTests     = 3
)

// validContextImpactFormat reports whether format is a supported
// context_impact output format ("" means the default JSON report).
func validContextImpactFormat(format string) bool {
	switch format {
	case "", contextFormatJSON, contextFormatCompact:
		return true
	default:
		return false
	}
}

// renderContextImpactCompact renders one line per candidate file, in ranker
// order, with its role and a one-line reason. Symbols and tests are counted,
// not listed, except for up to compactMaxTests validation commands. The text
// is deterministic: no timestamps.
func renderContextImpactCompact(pack comprehension.PackResult, limit int) string {
	if limit <= 0 {
		limit = compactDefaultFiles
	}

	var b strings.Builder

	fmt.Fprintf(&b, "context_impact index_id=%s fresh=%t view=%s\n", pack.IndexID, pack.Fresh, pack.ArtifactPath)

	files := pack.CandidateFiles
	if len(files) > limit {
		files = files[:limit]
	}

	for i, file := range files {
		fmt.Fprintf(&b, "%d %s  %s  %s\n", i+1, file.Path, file.Kind, file.Why)
	}

	shown := 0

	for _, test := range pack.Tests {
		if test.Command == "" || shown == compactMaxTests {
			continue
		}

		fmt.Fprintf(&b, "test %s\n", test.Command)

		shown++
	}

	if omitted := len(pack.CandidateFiles) - len(files); omitted > 0 {
		fmt.Fprintf(&b, "+%d more files; %d symbols; omit format=compact for the full report\n", omitted, len(pack.CandidateSymbols))
	}

	return b.String()
}
