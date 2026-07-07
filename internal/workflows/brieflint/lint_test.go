package brieflint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedPreflightScriptHasUsageAnchor(t *testing.T) {
	script, err := preflightFS.ReadFile("brief-preflight.py")
	if err != nil {
		t.Fatalf("read embedded preflight: %v", err)
	}

	if !strings.Contains(string(script), "brief-preflight.py <brief.md>") {
		t.Fatalf("brief preflight script is missing its usage anchor")
	}
}

func TestParseFindingsCategorizesByKeyword(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Finding
	}{
		{
			name: "anchor-specificity-block",
			raw:  "idiom brief preflight: FAIL /b.md\n- exact-anchor specificity failed for x.go.\n",
			want: Finding{Severity: "BLOCK", Category: "anchor-specificity", Brief: "/b.md", Message: "exact-anchor specificity failed for x.go."},
		},
		{
			name: "warn-current-state",
			raw:  "- WARN: no obvious verified-current-state section found.\n",
			want: Finding{Severity: "WARN", Category: "missing-current-state", Brief: "/b.md", Message: "no obvious verified-current-state section found."},
		},
		{
			name: "exit-zero-warning",
			raw:  "idiom brief preflight: OK /b.md\nidiom brief preflight: WARN branching language in Required Edit; replace with one unconditional edit\n",
			want: Finding{Severity: "WARN", Category: "branching-language", Brief: "/b.md", Message: "branching language in Required Edit; replace with one unconditional edit"},
		},
		{
			name: "info",
			raw:  "- INFO: noteworthy detail\n",
			want: Finding{Severity: "INFO", Category: "other", Brief: "/b.md", Message: "noteworthy detail"},
		},
		{
			name: "placeholder",
			raw:  "- some unfilled placeholder somewhere\n",
			want: Finding{Severity: "BLOCK", Category: "unfilled-placeholder", Brief: "/b.md", Message: "some unfilled placeholder somewhere"},
		},
		{
			name: "unknown-block",
			raw:  "- something nobody planned for\n",
			want: Finding{Severity: "BLOCK", Category: "other", Brief: "/b.md", Message: "something nobody planned for"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseFindings("/b.md", tc.raw)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("ParseFindings = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRunEmbeddedPreflightUsesWorkspaceDirectory(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available on PATH")
	}

	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}

	briefPath := filepath.Join(workspace, "brief.md")
	body := "# Brief\n\n" +
		"## Files to Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `foo.go:1` | Read module declaration. |\n\n" +
		"## Verified Anchors\n\n" +
		"- `foo.go:1`: package main\n\n" +
		"## Required Edits\n\n" +
		"Read the file without changing it.\n\n" +
		"## Acceptance\n\n" +
		"- `go test ./...` succeeds.\n"
	if err := os.WriteFile(briefPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	result, err := RunEmbeddedPreflight(context.Background(), briefPath, workspace, 0)
	if err != nil {
		t.Fatalf("RunEmbeddedPreflight: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("preflight failed: err=%v stdout=%s stderr=%s", result.Err, result.Stdout, result.Stderr)
	}
}
