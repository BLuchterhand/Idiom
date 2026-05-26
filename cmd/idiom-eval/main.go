package main

import (
	"os"

	"idiom/internal/evalcmd"
)

func main() {
	os.Exit(evalcmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
