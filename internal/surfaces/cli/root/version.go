package root

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// version is supplied by cmd/idiom from its ldflags-injected main.version
// value. Default value "dev" keeps root command tests and direct internal
// callers identifiable.
var version = "dev"

// commit is supplied by cmd/idiom from its ldflags-injected main.commit value.
// Default empty so direct internal callers do not claim a fake commit; the
// runtime fallback below reads debug.ReadBuildInfo() when available.
var commit = ""

// date is supplied by cmd/idiom from its ldflags-injected main.date value.
// Default empty for the same reason as commit.
var date = ""

// runVersionMain implements the `idiom version` subcommand. It prints
// the binary version, commit hash (if known), build date (if known),
// and the Go runtime version. Format is intentionally machine-parseable
// — keys on the left, values on the right, one per line — so wrappers
// can grep for "version:" without parsing prose.
//
// Falls back to debug.ReadBuildInfo() for commit/date when the ldflags
// weren't injected, so a `go install ./...` build still reports the
// VCS state Go's toolchain captured.
func runVersionMain(_ []string, stdout io.Writer, _ io.Writer) int {
	resolvedCommit, resolvedDate := commit, date
	if resolvedCommit == "" || resolvedDate == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					if resolvedCommit == "" {
						resolvedCommit = s.Value
					}
				case "vcs.time":
					if resolvedDate == "" {
						resolvedDate = s.Value
					}
				}
			}
		}
	}

	fmt.Fprintf(stdout, "version: %s\n", version)

	if resolvedCommit != "" {
		fmt.Fprintf(stdout, "commit:  %s\n", resolvedCommit)
	}

	if resolvedDate != "" {
		fmt.Fprintf(stdout, "date:    %s\n", resolvedDate)
	}

	fmt.Fprintf(stdout, "go:      %s\n", runtime.Version())
	fmt.Fprintf(stdout, "os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)

	return 0
}
