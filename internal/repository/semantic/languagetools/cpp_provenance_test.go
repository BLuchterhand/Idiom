package languagetools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	semanticcpp "idiom/internal/repository/semantic/cpp"
)

func gitIn(t *testing.T, root string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = root

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The index key must change whenever the indexed content can change: a dirty
// tracked file, an untracked file and a gitignored C++ file the walk still reads.
func TestCppSnapshotKeyCoversDirtyAndIgnoredFiles(t *testing.T) {
	root := t.TempDir()
	gitIn(t, root, "init", "-q")
	writeFile(t, filepath.Join(root, ".gitignore"), "ign/\n")
	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 1; }\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "init")

	key := func() string {
		info := cppSnapshot(context.Background(), root)
		if info.GitError != "" || info.Key == "" {
			t.Fatalf("snapshot failed: %q", info.GitError)
		}

		return info.Key
	}

	clean := key()
	if key() != clean {
		t.Fatal("key is not stable for an unchanged tree")
	}

	writeFile(t, filepath.Join(root, "new.cpp"), "int new_symbol() { return 1; }\n")
	withNew := key()
	if withNew == clean {
		t.Fatal("a new untracked C++ file does not change the key")
	}
	if err := os.Remove(filepath.Join(root, "new.cpp")); err != nil {
		t.Fatal(err)
	}
	if got := key(); got != clean {
		t.Fatalf("deleting the new C++ file did not restore the key: got %q want %q", got, clean)
	}

	if err := os.Remove(filepath.Join(root, "a.cpp")); err != nil {
		t.Fatal(err)
	}
	withoutTracked := key()
	if withoutTracked == clean {
		t.Fatal("deleting a tracked C++ file does not change the key")
	}
	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 1; }\n")
	if got := key(); got != clean {
		t.Fatalf("restoring the deleted C++ file did not restore the key: got %q want %q", got, clean)
	}

	writeFile(t, filepath.Join(root, "ign", "x.h"), "int x();\n")

	withIgnored := key()
	if withIgnored == clean {
		t.Fatal("a gitignored C++ file does not change the key")
	}

	writeFile(t, filepath.Join(root, "ign", "x.h"), "int y();\n")

	if key() == withIgnored {
		t.Fatal("editing a gitignored C++ file does not change the key")
	}

	beforeSkipped := key()
	writeFile(t, filepath.Join(root, ".claude", "skip.h"), "int skipped();\n")
	if key() != beforeSkipped {
		t.Fatal("a C++ file under a discovery-excluded directory changed the key")
	}

	before := key()

	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 2; }\n")

	if key() == before {
		t.Fatal("editing a tracked C++ file does not change the key")
	}
}

func TestCompileCommandsDirFindsDeterministicPerBuildDatabase(t *testing.T) {
	root := t.TempDir()
	directory := filepath.ToSlash(root)
	writeFile(t, filepath.Join(root, "build", "zeta", "compile_commands.json"), `[{"directory":"`+directory+`","file":"`+filepath.ToSlash(filepath.Join(root, "other", "zeta.cpp"))+`"}]`+"\n")
	writeFile(t, filepath.Join(root, "build", "alpha", "compile_commands.json"), `[{"directory":"`+directory+`","file":"`+filepath.ToSlash(filepath.Join(root, "core", "alpha.cpp"))+`"}]`+"\n")
	writeFile(t, filepath.Join(root, "build", "beta", "compile_commands.json"), `[{"directory":"`+directory+`","file":"`+filepath.ToSlash(filepath.Join(root, "core", "alpha.cpp"))+`"}]`+"\n")

	tieTime := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	for _, path := range []string{
		filepath.Join(root, "build", "alpha", "compile_commands.json"),
		filepath.Join(root, "build", "beta", "compile_commands.json"),
	} {
		if err := os.Chtimes(path, tieTime, tieTime); err != nil {
			t.Fatal(err)
		}
	}

	want := filepath.Join(root, "build", "alpha")
	if got := compileCommandsDirForRoot(root); got != want {
		t.Fatalf("compile database directory = %q, want %q", got, want)
	}
}

func TestCppSnapshotFastProbeInvalidatesTrackedUntrackedIgnoredAndRuleChanges(t *testing.T) {
	root := t.TempDir()
	gitIn(t, root, "init", "-q")
	writeFile(t, filepath.Join(root, ".gitignore"), "ign/\n")
	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 1; }\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "init")

	info := cppSnapshot(context.Background(), root)
	if info.GitError != "" {
		t.Fatal(info.GitError)
	}
	probe, err := newCppSnapshotProbe(context.Background(), root, info)
	if err != nil {
		t.Fatal(err)
	}
	assertProbeUnchanged := func(want bool) {
		t.Helper()
		unchanged, _, probeErr := cppSnapshotFastUnchanged(context.Background(), root, probe)
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		if unchanged != want {
			t.Fatalf("probe unchanged = %v, want %v", unchanged, want)
		}
	}

	assertProbeUnchanged(true)
	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 2; }\n")
	assertProbeUnchanged(false)
	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 1; }\n")
	if err := os.Remove(filepath.Join(root, "a.cpp")); err != nil {
		t.Fatal(err)
	}
	assertProbeUnchanged(false)
	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 1; }\n")

	writeFile(t, filepath.Join(root, "new.cpp"), "int new_symbol();\n")
	assertProbeUnchanged(false)
	if err := os.Remove(filepath.Join(root, "new.cpp")); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(root, "ign", "x.h"), "int x();\n")
	assertProbeUnchanged(false)
	info = cppSnapshot(context.Background(), root)
	probe, err = newCppSnapshotProbe(context.Background(), root, info)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "ign", "x.h"), "int y();\n")
	assertProbeUnchanged(false)
	writeFile(t, filepath.Join(root, ".gitignore"), "ign/\nother/\n")
	assertProbeUnchanged(false)
	if err := os.Remove(filepath.Join(root, "ign", "x.h")); err != nil {
		t.Fatal(err)
	}
	assertProbeUnchanged(false)
}

func BenchmarkCppSnapshot(b *testing.B) {
	root := os.Getenv("IDIOM_CPP_BENCH_ROOT")
	if root == "" {
		b.Skip("set IDIOM_CPP_BENCH_ROOT to benchmark a real workspace")
	}

	b.ReportAllocs()
	for range b.N {
		info := cppSnapshot(context.Background(), root)
		if info.GitError != "" || info.Key == "" {
			b.Fatal(info.GitError)
		}
	}
}

func BenchmarkCppSnapshotCachedHit(b *testing.B) {
	root := os.Getenv("IDIOM_CPP_BENCH_ROOT")
	if root == "" {
		b.Skip("set IDIOM_CPP_BENCH_ROOT to benchmark a real workspace")
	}

	info := cppSnapshot(context.Background(), root)
	if info.GitError != "" || info.Key == "" {
		b.Fatal(info.GitError)
	}
	index, err := semanticcpp.BuildCppIndex(root)
	if err != nil || index == nil {
		b.Fatal(err)
	}
	for _, file := range index.Files {
		info.IndexedPaths = append(info.IndexedPaths, file.Path)
	}
	probe, err := newCppSnapshotProbe(context.Background(), root, info)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("probe files=%d dirs=%d rules=%d", len(probe.Files), len(probe.Directories), len(probe.IgnoreFiles))
	b.ReportMetric(float64(len(probe.Files)), "probe_files")
	b.ReportMetric(float64(len(probe.Directories)), "probe_dirs")
	b.ReportMetric(float64(len(probe.IgnoreFiles)), "probe_rules")

	var total time.Duration
	b.ResetTimer()
	for range b.N {
		unchanged, cost, err := cppSnapshotFastUnchanged(context.Background(), root, probe)
		if err != nil {
			b.Fatal(err)
		}
		if !unchanged {
			b.Fatal("stable workspace did not pass the cache-hit probe")
		}
		total += cost
	}
	b.StopTimer()
	b.ReportMetric(total.Seconds()*1000/float64(b.N), "probe_ms")
}
