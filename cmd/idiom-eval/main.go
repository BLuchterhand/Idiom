package main

import (
	"os"

	evalcmd "idiom/internal/cmd/eval"
)

func main() {
	os.Exit(evalcmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
