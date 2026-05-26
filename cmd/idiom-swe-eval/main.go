package main

import (
	"os"

	"idiom/internal/sweevalcmd"
)

func main() {
	os.Exit(sweevalcmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
