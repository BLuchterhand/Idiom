package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindGNUStyleGrepProbesVersionOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable shell fixture uses POSIX mode bits")
	}

	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "accepts GNU grep marker on stdout",
			body: "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\n",
			want: true,
		},
		{
			name: "accepts GNU grep marker on stderr",
			body: "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11' >&2\n",
			want: true,
		},
		{
			name: "rejects non-GNU grep output",
			body: "#!/bin/sh\nprintf '%s\\n' 'grep (BSD grep) 2.6'\n",
			want: false,
		},
		{
			name: "rejects failing probe even with GNU output",
			body: "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\nexit 2\n",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeExecutableSearchBackendProbe(t, tt.body)

			gotPath, got := findGNUStyleGrep(path)
			if got != tt.want {
				t.Fatalf("findGNUStyleGrep ok = %v, want %v", got, tt.want)
			}
			if got && gotPath != path {
				t.Fatalf("findGNUStyleGrep path = %q, want %q", gotPath, path)
			}
		})
	}
}

func writeExecutableSearchBackendProbe(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "grep")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	return path
}
