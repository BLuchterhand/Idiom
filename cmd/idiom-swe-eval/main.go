package main

import (
	"os"

	sweevalcmd "idiom/internal/cmd/sweeval"
)

func main() {
	os.Exit(sweevalcmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
