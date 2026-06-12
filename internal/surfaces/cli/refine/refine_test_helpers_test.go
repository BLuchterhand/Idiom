package refine

import (
	"io"
	"testing"
)

func stubLaneBriefLint(t *testing.T, stub func(args []string, stdout, stderr io.Writer) int) {
	t.Helper()

	orig := laneBriefLintRunner

	laneBriefLintRunner = stub

	t.Cleanup(func() { laneBriefLintRunner = orig })
}
