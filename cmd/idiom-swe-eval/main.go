package main

import (
	"os"

	sweevalcmd "idiom/internal/surfaces/cli/sweeval"
)

func main() {
	os.Exit(sweevalcmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
