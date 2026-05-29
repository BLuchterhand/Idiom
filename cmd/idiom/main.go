package main

import (
	"os"

	rootcmd "idiom/internal/cmd/root"
)

// version is set at build time via -ldflags "-X main.version=v1.2.3".
// Default value "dev" is what `go run`/`go build` without ldflags produces.
var version = "dev"

// commit and date are set at build time via ldflags by the release pipeline.
var commit = ""
var date = ""

func main() {
	os.Exit(rootcmd.RunMain(os.Args[1:], os.Stdout, os.Stderr, rootcmd.Options{
		Version: version,
		Commit:  commit,
		Date:    date,
	}))
}
