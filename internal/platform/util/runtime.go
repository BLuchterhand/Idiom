package util

import (
	"os"
	"path/filepath"
	"strings"
)

// RunningUnderGoTest reports whether the current process is a `go test` binary.
// Production code uses this only to keep tests from touching user-owned state.
func RunningUnderGoTest() bool {
	name := filepath.Base(os.Args[0])
	return strings.HasSuffix(name, ".test")
}
