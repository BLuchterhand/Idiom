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

func TestResolveContentSearchBackendUsesHostToolLookup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable shell fixture uses POSIX mode bits")
	}

	tests := []struct {
		name        string
		executables map[string]string
		wantBin     string
		wantKind    string
	}{
		{
			name: "prefers ripgrep",
			executables: map[string]string{
				"rg":   "#!/bin/sh\nexit 0\n",
				"grep": "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\n",
			},
			wantBin:  "rg",
			wantKind: contentSearchRipgrep,
		},
		{
			name: "accepts GNU grep",
			executables: map[string]string{
				"grep": "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\n",
			},
			wantBin:  "grep",
			wantKind: contentSearchGrep,
		},
		{
			name: "falls back to ggrep when plain grep is not GNU",
			executables: map[string]string{
				"grep":  "#!/bin/sh\nprintf '%s\\n' 'grep (BSD grep) 2.6'\n",
				"ggrep": "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\n",
			},
			wantBin:  "ggrep",
			wantKind: contentSearchGrep,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			for name, body := range tt.executables {
				path := filepath.Join(binDir, name)
				if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			t.Setenv("PATH", binDir)

			got, err := resolveContentSearchBackend()
			if err != nil {
				t.Fatalf("resolveContentSearchBackend: %v", err)
			}
			if got.kind != tt.wantKind || filepath.Base(got.bin) != tt.wantBin {
				t.Fatalf("backend = %#v, want bin %q kind %q", got, tt.wantBin, tt.wantKind)
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
