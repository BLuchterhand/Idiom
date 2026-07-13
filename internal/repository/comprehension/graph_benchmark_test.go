package comprehension

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkBuildFullGoRepository(b *testing.B) {
	root := benchmarkGoRepository(b, 40)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
			b.Fatalf("Build: %v", err)
		}
	}
}

func BenchmarkBuildExactScopeIncremental(b *testing.B) {
	root := benchmarkGoRepository(b, 40)
	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		b.Fatalf("initial Build: %v", err)
	}

	target := filepath.Join(root, "pkg00", "file.go")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		source := fmt.Sprintf("package pkg00\n\nfunc Value() int { return %d }\n", i)
		if err := os.WriteFile(target, []byte(source), 0o644); err != nil {
			b.Fatalf("update target: %v", err)
		}
		if _, err := Build(context.Background(), BuildOptions{
			Root:       root,
			Path:       "pkg00/file.go",
			Language:   "go",
			ExactScope: true,
		}); err != nil {
			b.Fatalf("incremental Build: %v", err)
		}
	}
}

func benchmarkGoRepository(tb testing.TB, packages int) string {
	tb.Helper()

	root := tb.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/benchmark\n\ngo 1.22\n"), 0o644); err != nil {
		tb.Fatalf("write go.mod: %v", err)
	}

	for i := 0; i < packages; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("create package: %v", err)
		}
		source := fmt.Sprintf("package pkg%02d\n\nfunc Value() int { return %d }\n", i, i)
		if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte(source), 0o644); err != nil {
			tb.Fatalf("write source: %v", err)
		}
	}

	return root
}
