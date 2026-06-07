// Package profilecmd implements the `idiom profile` subcommand for
// inspecting and changing the active backend profile.
package profile

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/runtime/backends"
)

// Run dispatches `idiom profile <subcommand>`.
func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printUsage(stderr)
		return 0
	}
	switch args[0] {
	case "set":
		return runSet(args[1:], stdout, stderr)
	case "show":
		return runShow(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "profile: unknown subcommand %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runSet(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom profile set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var projectRoot string
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/backends.json (defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	name := strings.TrimSpace(fs.Arg(0))
	if name == "" {
		fmt.Fprintln(stderr, "profile set: requires a profile name")
		return 2
	}

	root := projectRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		root = cwd
	}
	path := filepath.Join(root, ".idiom", "backends.json")

	file, ok, err := backends.LoadFromProjectRoot(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(stderr, "profile set: no .idiom/backends.json under %s\n", root)
		return 1
	}
	if _, exists := file.Profiles[name]; !exists {
		fmt.Fprintf(stderr, "profile set: profile %q not found\n", name)
		return 1
	}

	file.DefaultProfile = name
	if err := file.Save(path); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "default profile set to %q\n", name)
	return 0
}

func runShow(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom profile show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var projectRoot string
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/backends.json (defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := projectRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		root = cwd
	}
	file, ok, err := backends.LoadFromProjectRoot(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(stderr, "profile show: no .idiom/backends.json under %s\n", root)
		return 1
	}
	if file.DefaultProfile == "" {
		fmt.Fprintln(stdout, "(no default profile set)")
	} else {
		fmt.Fprintln(stdout, file.DefaultProfile)
	}
	return 0
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: idiom profile <subcommand> [flags]")
	fmt.Fprintln(stderr, "subcommands:")
	fmt.Fprintln(stderr, "  set <name>     set the default profile in .idiom/backends.json")
	fmt.Fprintln(stderr, "  show           show the current default profile")
	fmt.Fprintln(stderr, "flags:")
	fmt.Fprintln(stderr, "  --project-root <path>    project root (defaults to cwd)")
}
