package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/backends"
	"idiom/internal/telemetry"
)

// runBackendMain dispatches `idiom backend <subcommand>`. Phase 4
// ships `list` (introspect the loaded backends.json) so projects
// have visibility into what idiom is reading; `start`, `stop`, and
// `proxy` ship in Phase 4b once the launcher port is finalized.
func runBackendMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runBackendMainWithTelemetry(args, stdout, stderr, nil)
}

func runBackendMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	logCLIPhase(rec, "backend_start", map[string]any{"argc": len(args)})
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printBackendUsage(stderr)
		return 0
	}
	switch args[0] {
	case "list":
		return runBackendList(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "backend: unknown subcommand %q\n", args[0])
		printBackendUsage(stderr)
		return 2
	}
}

// runBackendList prints the parsed contents of .idiom/backends.json
// in a stable, human-readable form. It's the read-only escape hatch
// projects use to confirm idiom sees the file they expect.
func runBackendList(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom backend list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectRoot string
		path        string
	)
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/backends.json (defaults to cwd)")
	fs.StringVar(&path, "file", "", "explicit path to a backends.json (overrides --project-root)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var (
		file backends.File
		ok   bool
		err  error
		src  string
	)
	switch {
	case strings.TrimSpace(path) != "":
		src = path
		file, err = backends.Load(path)
		if err == nil {
			ok = true
		}
	default:
		root := projectRoot
		if strings.TrimSpace(root) == "" {
			cwd, cwdErr := os.Getwd()
			if cwdErr != nil {
				fmt.Fprintln(stderr, cwdErr)
				return 1
			}
			root = cwd
		}
		src = filepath.Join(root, ".idiom", "backends.json")
		file, ok, err = backends.LoadFromProjectRoot(root)
	}

	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(stderr, "backend: no backends.json at %s\n", src)
		return 0
	}

	formatBackendsList(stdout, src, file)
	return 0
}

// formatBackendsList writes a deterministic, sorted summary of the
// loaded file. Profile names sort alphabetically so the output is
// stable across runs (Go map iteration is randomized).
func formatBackendsList(stdout io.Writer, src string, file backends.File) {
	fmt.Fprintf(stdout, "%s\n", src)
	if len(file.Profiles) == 0 {
		fmt.Fprintln(stdout, "  (no profiles declared)")
		return
	}
	names := make([]string, 0, len(file.Profiles))
	for name := range file.Profiles {
		names = append(names, name)
	}
	// Avoid pulling in sort just for this — manual ascending insert is
	// fine for the handful of profiles a project realistically has.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	for _, name := range names {
		prof := file.Profiles[name]
		fmt.Fprintf(stdout, "profile %q\n", name)
		if prof.Proxy.Listen != 0 || prof.Proxy.DefaultAlias != "" || prof.Proxy.DefaultCoder != "" {
			fmt.Fprintf(stdout, "  proxy listen=%d default=%q default_coder=%q\n", prof.Proxy.Listen, prof.Proxy.DefaultAlias, prof.Proxy.DefaultCoder)
		}
		if coder := prof.CoderAlias(); coder != "" {
			fmt.Fprintf(stdout, "  coder_alias=%q\n", coder)
		}
		for _, m := range prof.Models {
			fmt.Fprintf(stdout, "  model alias=%q binary=%q port=%d max_tokens=%d\n", m.Alias, m.Binary, m.Port, m.MaxTokens)
		}
	}
}

func printBackendUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: idiom backend <subcommand> [flags]")
	fmt.Fprintln(stderr, "subcommands:")
	fmt.Fprintln(stderr, "  list            print the parsed .idiom/backends.json")
	fmt.Fprintln(stderr, "                  flags: --project-root <path> | --file <path>")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Phase 4b will add: start, stop, proxy.")
}
