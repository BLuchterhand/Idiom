package sidecarvalidation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"idiom/internal/evidence/productevidence"
	"idiom/internal/surfaces/cli/config"
	localopscmd "idiom/internal/surfaces/cli/localops"
	toolcmd "idiom/internal/surfaces/cli/tool"
)

var p98RequiredMCPParityReadinessFields = productevidence.P98RequiredMCPParityReadinessFields()

func writeMainConfig(t testFataler) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func runMain(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "missing command")
		return 2
	}
	switch args[0] {
	case "tool":
		return toolcmd.Run(args[1:], stdout, stderr)
	case "local-ops":
		return localopscmd.RunMainWithTelemetry(args[1:], stdout, stderr, nil, localopscmd.Dependencies{
			Version:    "test",
			Commit:     "test",
			LoadConfig: cmdconfig.Load,
		})
	default:
		fmt.Fprintf(stderr, "unsupported proof command %q\n", args[0])
		return 2
	}
}

func writeMainConfigRoot(t testFataler, root string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	if err := os.WriteFile(path, []byte(configJSON+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func stageDoctorConfigWithBudget(t testFataler, root string, ctxSize int, maxTokens int) {
	t.Helper()
	cfg := filepath.Join(root, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := fmt.Sprintf(`{
  "workspace": {"root": %q, "cwd": %q},
  "llama_server": {"ctx_size": %d},
  "chat": {"max_tokens": %d}
}`, root, root, ctxSize, maxTokens)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}
}

type testFataler interface {
	Helper()
	TempDir() string
	Fatalf(format string, args ...any)
}
