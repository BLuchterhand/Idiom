package main

import (
	"os"

	perfevalcmd "idiom/internal/cmd/perfeval"
)

func main() {
	os.Exit(perfevalcmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
