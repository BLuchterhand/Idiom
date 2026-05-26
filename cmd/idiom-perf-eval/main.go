package main

import (
	"os"

	"idiom/internal/perfevalcmd"
)

func main() {
	os.Exit(perfevalcmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
