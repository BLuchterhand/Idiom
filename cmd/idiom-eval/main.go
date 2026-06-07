package main

import (
	"os"

	evalcmd "idiom/internal/surfaces/cli/eval"
)

func main() {
	os.Exit(evalcmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
