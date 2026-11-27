package languagetools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"go.uber.org/mock/gomock"

	semanticcpp "idiom/internal/repository/semantic/cpp"
)

func cppTestManager(t GinkgoTInterface, root string) *Manager {
	t.Helper()
	host := NewMockHost(gomock.NewController(t))
	host.EXPECT().Enabled().Return(true).AnyTimes()
	host.EXPECT().Root().Return(root).AnyTimes()
	return NewManager(host)
}

func cppTestRepo(t GinkgoTInterface) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo with spaces")
	writeFile(t, filepath.Join(root, "a.h"), "void target();\n")
	writeFile(t, filepath.Join(root, "a.cpp"), "#include \"a.h\"\nvoid target() {}\nvoid caller() { target(); }\n")
	writeFile(t, filepath.Join(root, "b.cpp"), "#include \"a.h\"\nvoid other() { target(); }\n")
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "init")
	return root
}

type cppProcessResult struct {
	Info  cppIndexInfo
	Root  string
	Calls int
}

// This is executed by a fresh test process, proving that no Manager state is
// responsible for a hit. Failures propagate through the process exit status.
var _ = It("C++ cache process helper", func() {
	t := GinkgoT()
	root := os.Getenv("IDIOM_CPP_CACHE_TEST_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	index, info, err := cppTestManager(t, root).loadCppIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	calls := targetCallerCount(index)
	result := cppProcessResult{Info: info, Root: index.Root, Calls: calls}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("IDIOM_CPP_CACHE_TEST_OUTPUT"), data, 0o600); err != nil {
		t.Fatal(err)
	}
})

func cppTestProcess(t GinkgoTInterface, root string) (*exec.Cmd, string) {
	t.Helper()
	output := filepath.Join(t.TempDir(), "result.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLanguageTools$", "-ginkgo.focus=cache process helper", "-ginkgo.fail-on-empty", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), "IDIOM_CPP_CACHE_TEST_ROOT="+root, "IDIOM_CPP_CACHE_TEST_OUTPUT="+output)
	return cmd, output
}

func cppReadProcess(t GinkgoTInterface, path string) cppProcessResult {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result cppProcessResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

var _ = It("C++ cache reuses indexes across processes and worktrees", func() {
	t := GinkgoT()
	t.Setenv("IDIOM_CACHE_DIR", t.TempDir())
	root := cppTestRepo(t)
	cmd, output := cppTestProcess(t, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cold: %v %s", err, out)
	}
	cold := cppReadProcess(t, output)
	if cold.Info.Cache == "disk_hit" || cold.Calls != 2 {
		t.Fatalf("cold: %+v", cold)
	}
	for _, worktree := range []bool{false, true} {
		target := root
		if worktree {
			target = filepath.Join(t.TempDir(), "second worktree")
			gitIn(t, root, "worktree", "add", "--detach", target, "HEAD")
		}
		cmd, output = cppTestProcess(t, target)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("warm: %v %s", err, out)
		}
		warm := cppReadProcess(t, output)
		if warm.Info.Cache != "disk_hit" || warm.Info.Key != cold.Info.Key || warm.Calls != 2 || warm.Root != target || warm.Info.Root != target {
			t.Fatalf("worktree=%t: %+v", worktree, warm)
		}
	}
})

var _ = It("C++ cache publishes safely from concurrent processes", func() {
	t := GinkgoT()
	t.Setenv("IDIOM_CACHE_DIR", t.TempDir())
	root := cppTestRepo(t)
	// More files make independent builders overlap before publication.
	for i := 0; i < 30; i++ {
		writeFile(t, filepath.Join(root, fmt.Sprintf("extra%d.cpp", i)), strings.Repeat("// padding\n", 400)+fmt.Sprintf("int f%d() { return 0; }\n", i))
	}
	cmdA, outputA := cppTestProcess(t, root)
	cmdB, outputB := cppTestProcess(t, root)
	var logA, logB bytes.Buffer
	cmdA.Stdout, cmdA.Stderr = &logA, &logA
	cmdB.Stdout, cmdB.Stderr = &logB, &logB
	if err := cmdA.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmdB.Start(); err != nil {
		_ = cmdA.Wait()
		t.Fatal(err)
	}
	errA, errB := cmdA.Wait(), cmdB.Wait()
	if errA != nil || errB != nil {
		t.Fatalf("builders: %v, %v\n%s\n%s", errA, errB, logA.String(), logB.String())
	}
	a, b := cppReadProcess(t, outputA), cppReadProcess(t, outputB)
	if a.Info.Key != b.Info.Key || a.Calls != 2 || b.Calls != 2 {
		t.Fatalf("inconsistent builders: %+v %+v", a, b)
	}
	_, info, err := cppTestManager(t, root).loadCppIndex(context.Background())
	if err != nil || info.Cache != "disk_hit" || info.Build.Files != 33 {
		t.Fatalf("published: %+v %v", info, err)
	}
})

var _ = It("C++ cache rebuilds corrupt and incompatible entries", func() {
	t := GinkgoT()
	for _, fault := range []string{"truncated", "checksum", "format", "collector"} {
		{
			dir := t.TempDir()
			t.Setenv("IDIOM_CACHE_DIR", dir)
			root := cppTestRepo(t)
			_, info, err := cppTestManager(t, root).loadCppIndex(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "cpp", info.Key+".index")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "truncated":
				data = data[:len(data)/2]
			case "checksum":
				data[len(data)-2] ^= 1
			case "format":
				data = []byte(strings.Replace(string(data), `"Format":1`, `"Format":0`, 1))
			case "collector":
				data = []byte(strings.Replace(string(data), cppCollectorVersion, "old-collector", 1))
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			index, rebuilt, err := cppTestManager(t, root).loadCppIndex(context.Background())
			if err != nil || rebuilt.Cache == "disk_hit" || len(index.Files) != 3 {
				t.Fatalf("rebuild: %+v %v", rebuilt, err)
			}
			_, hit, err := cppTestManager(t, root).loadCppIndex(context.Background())
			if err != nil || hit.Cache != "disk_hit" {
				t.Fatalf("subsequent hit: %+v %v", hit, err)
			}
		}
	}
})

var _ = It("C++ cache reparses changes and relinks moved headers", func() {
	t := GinkgoT()
	t.Setenv("IDIOM_CACHE_DIR", t.TempDir())
	root := cppTestRepo(t)
	m := cppTestManager(t, root)
	_, original, err := m.loadCppIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a.cpp"), "#include \"a.h\"\nvoid replacement() {}\n")
	index, dirty, err := m.loadCppIndex(context.Background())
	if err != nil || dirty.Key == original.Key || dirty.Cache != "incremental" || dirty.Build.Parsed != 1 || dirty.Build.Reused != 2 {
		t.Fatalf("dirty: %+v %v", dirty, err)
	}
	if calls := targetCallerCount(index); calls != 1 {
		t.Fatal("dirty source was stale")
	}
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "change")
	if err := os.Remove(filepath.Join(root, "a.h")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "include", "a.h"), "void replacement();\n")
	index, committed, err := cppTestManager(t, root).loadCppIndex(context.Background())
	if err != nil || committed.Cache != "incremental" || committed.Build.Parsed != 1 {
		t.Fatalf("commit: %+v %v", committed, err)
	}
	full, err := semanticcpp.BuildCppIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(index.Files, full.Files) || !reflect.DeepEqual(index.Forward, full.Forward) || !reflect.DeepEqual(index.Reverse, full.Reverse) {
		t.Fatal("incremental result differs from a full rebuild after header relocation")
	}
})

var _ = It("C++ cache evicts least recently used entries and preserves complete indexes", func() {
	t := GinkgoT()
	t.Setenv("IDIOM_CACHE_DIR", t.TempDir())
	cache, err := openCppDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	defer cache.root.Close()
	index := &semanticcpp.CppIndex{Files: []semanticcpp.CppFile{{Path: "a.cpp"}}}
	put := func(n int, complete bool) string {
		t.Helper()
		key := fmt.Sprintf("%016x", n)
		if err := cache.store(cppIndexInfo{Key: key}, index, complete); err != nil {
			t.Fatal(err)
		}
		return key
	}
	first, second := put(1, true), put(2, true)
	stat, err := cache.root.Stat(first + ".index")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("IDIOM_CPP_CACHE_MAX_BYTES", fmt.Sprint(stat.Size()*2+10))
	old := time.Now().Add(-time.Hour)
	if err := cache.root.Chtimes(second+".index", old, old); err != nil {
		t.Fatal(err)
	}
	cache.touch(first)
	third := put(3, true)
	if _, _, err := cache.read(second, true); !os.IsNotExist(err) {
		t.Fatalf("LRU entry survived: %v", err)
	}
	for _, key := range []string{first, third} {
		if _, _, err := cache.read(key, true); err != nil {
			t.Fatal(err)
		}
	}
	// An independently finishing partial builder must not downgrade a full hit.
	put(3, false)
	h, _, err := cache.read(third, true)
	if err != nil || !h.Complete {
		t.Fatalf("partial replaced complete: %v", err)
	}
	entries, err := cache.entries()
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".index") {
			s, _ := e.Info()
			bytes += s.Size()
		}
	}
	if bytes > cppCacheMaxBytes() {
		t.Fatalf("cache exceeds bound: %d", bytes)
	}
})

var _ = It("C++ cache resumes checkpoints in another process", func() {
	t := GinkgoT()
	t.Setenv("IDIOM_CACHE_DIR", t.TempDir())
	root := cppTestRepo(t)
	index, err := semanticcpp.BuildCppIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	index.Files = index.Files[:1]
	cache, err := openCppDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	info := cppSnapshot(context.Background(), root)
	if err := cache.store(info, index, false); err != nil {
		t.Fatal(err)
	}
	cache.root.Close()
	cmd, output := cppTestProcess(t, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("resume process: %v %s", err, out)
	}
	resumed := cppReadProcess(t, output)
	if resumed.Info.Cache != "resumed" || resumed.Info.Build.Parsed != 2 || resumed.Info.Build.Reused != 1 || resumed.Calls != 2 {
		t.Fatalf("resume: %+v", resumed)
	}
})

var _ = It("C++ cache tracks ignored sources and excludes skipped directories", func() {
	t := GinkgoT()
	t.Setenv("IDIOM_CACHE_DIR", t.TempDir())
	root := cppTestRepo(t)
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored/\n.claude/\n")
	writeFile(t, filepath.Join(root, "ignored", "extra.cpp"), "void extra() {}\n")
	_, before, err := cppTestManager(t, root).loadCppIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".claude", "worktrees", "x", "wrong.cpp"), "void wrong() {}\n")
	_, skipped, err := cppTestManager(t, root).loadCppIndex(context.Background())
	if err != nil || skipped.Key != before.Key || skipped.Cache != "disk_hit" {
		t.Fatalf("excluded path invalidated index: %+v %v", skipped, err)
	}
	writeFile(t, filepath.Join(root, "ignored", "extra.cpp"), "void changed() {}\n")
	_, after, err := cppTestManager(t, root).loadCppIndex(context.Background())
	if err != nil || after.Key == before.Key || after.Build.Parsed != 1 || after.Build.Reused != 3 {
		t.Fatalf("ignored path was stale: %+v %v", after, err)
	}
})

var _ = It("C++ cache configures the indexing budget", func() {
	t := GinkgoT()
	for _, test := range []struct {
		value string
		want  time.Duration
	}{{"", cppDefaultIndexBudget}, {"bad", cppDefaultIndexBudget}, {"0s", cppDefaultIndexBudget}, {"-1s", cppDefaultIndexBudget}, {"90s", 90 * time.Second}} {
		t.Setenv("IDIOM_CPP_INDEX_BUDGET", test.value)
		if got := cppBudgetSetting("IDIOM_CPP_INDEX_BUDGET", cppDefaultIndexBudget); got != test.want {
			t.Fatalf("%q: got %s want %s", test.value, got, test.want)
		}
	}
})

var _ = It("C++ cache supports snapshots with hundreds of dirty source hashes", func() {
	t := GinkgoT()
	t.Setenv("IDIOM_CACHE_DIR", t.TempDir())
	cache, err := openCppDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	defer cache.root.Close()
	info := cppIndexInfo{Key: "0123456789abcdef", ContentHashes: make(map[string]string)}
	for i := 0; i < 1000; i++ {
		info.ContentHashes[fmt.Sprintf("src/file%d.cpp", i)] = strings.Repeat("a", 64)
	}
	if err := cache.store(info, &semanticcpp.CppIndex{}, true); err != nil {
		t.Fatal(err)
	}
	header, _, err := cache.read(info.Key, true)
	if err != nil || len(header.Info.ContentHashes) != 1000 {
		t.Fatalf("large snapshot metadata: %v", err)
	}
})

var _ = It("C++ cache returns typed failures from all five query tools", func() {
	t := GinkgoT()
	root := cppTestRepo(t)
	for _, tool := range []string{"cpp_calls_into", "cpp_references", "cpp_blast_radius", "cpp_who_includes", "cpp_find_symbol"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result := cppTestManager(t, root).Dispatch(ctx, ToolRequest{Tool: tool, Pattern: "target"})
		if result.Code != cppCodeBudgetExceeded || result.Error == "" || result.Output != "" {
			t.Fatalf("%s did not fail closed with a typed budget error: %+v", tool, result)
		}
	}
})

// targetCallerCount counts resolved call sites of the fixtures' free function
// target (main resolves callers through ResolveSymbol and CallsInto).
func targetCallerCount(index *semanticcpp.CppIndex) int {
	return index.CallsInto(index.ResolveSymbol("target", ""), "", 0).Total
}
