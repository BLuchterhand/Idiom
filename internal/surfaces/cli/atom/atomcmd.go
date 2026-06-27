package atom

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"idiom/internal/workflows/atomintake"
)

type Dependencies = atomintake.Dependencies

func RunMain(args []string, stdout io.Writer, stderr io.Writer, deps atomintake.Dependencies) int {
	if len(args) == 0 {
		printAtomUsage(stderr)

		return 2
	}

	switch args[0] {
	case "intake":
		return RunIntakeMain(args[1:], stdout, stderr, deps)
	case "-h", "--help", "help":
		printAtomUsage(stdout)

		return 0
	default:
		fmt.Fprintf(stderr, "idiom atom: unknown subcommand %q\n", args[0])
		printAtomUsage(stderr)

		return 2
	}
}

func printAtomUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  idiom atom intake --spec <atom.md> [--project-root <root>] [--json] [--allow-dirty]")
}

func RunIntakeMain(args []string, stdout io.Writer, stderr io.Writer, deps atomintake.Dependencies) int {
	opts := atomintake.IntakeOptions{}
	fs := flag.NewFlagSet("idiom atom intake", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.SpecPath, "spec", "", "atom/spec markdown file to classify before dispatch")
	fs.StringVar(&opts.ProjectRoot, "project-root", "", "workspace root for scoped path and dirty checks; default current directory")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable intake report")
	fs.BoolVar(&opts.AllowDirty, "allow-dirty", false, "allow dispatch even when scoped existing paths are dirty")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(opts.SpecPath) == "" {
		fmt.Fprintln(stderr, "atom intake: --spec is required")

		return 2
	}

	report, err := atomintake.RunIntake(opts, deps)
	if err != nil {
		fmt.Fprintf(stderr, "atom intake: %v\n", err)

		return 1
	}

	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "atom intake: cannot encode JSON: %v\n", err)

			return 1
		}
	} else {
		PrintIntakeReport(report, stdout)
	}

	if report.Dispatchable {
		return 0
	}

	return 1
}

func PrintIntakeReport(report atomintake.IntakeReport, w io.Writer) {
	fmt.Fprintf(w, "state: %s\n", report.State)

	if report.Status != "" {
		fmt.Fprintf(w, "status: %s\n", report.Status)
	}

	fmt.Fprintf(w, "dispatchable: %t\n", report.Dispatchable)

	if len(report.ScopePaths) > 0 {
		fmt.Fprintf(w, "scope: %s\n", strings.Join(report.ScopePaths, ", "))
	}

	if len(report.Dirty) > 0 {
		fmt.Fprintf(w, "dirty: %s\n", strings.Join(report.Dirty, ", "))
	}

	if len(report.Invalid) > 0 {
		fmt.Fprintf(w, "invalid: %s\n", strings.Join(report.Invalid, ", "))
	}

	for _, reason := range report.Reasons {
		fmt.Fprintf(w, "reason: %s\n", reason)
	}

	for _, action := range report.NextActions {
		fmt.Fprintf(w, "next: %s\n", action)
	}
}
