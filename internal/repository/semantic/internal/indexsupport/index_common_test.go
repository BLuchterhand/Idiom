package indexsupport

import "testing"

type testFile struct {
	path  string
	funcs int
}

func testPath(file testFile) string {
	return file.path
}

func TestRankFilesSortsByScore(t *testing.T) {
	files := []testFile{
		{path: "a.go", funcs: 1},
		{path: "b.go", funcs: 10},
		{path: "c.go", funcs: 5},
	}

	result := RankFiles(files, testPath, func(file testFile) int {
		return file.funcs
	}, 0)

	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result))
	}
	if result[0].path != "b.go" || result[1].path != "c.go" || result[2].path != "a.go" {
		t.Fatalf("unexpected ranking order: %#v", result)
	}
}

func TestRankFilesSkipsZeroAndNegative(t *testing.T) {
	files := []testFile{
		{path: "a.go", funcs: 5},
		{path: "b.go", funcs: 0},
		{path: "c.go", funcs: -1},
	}

	result := RankFiles(files, testPath, func(file testFile) int {
		return file.funcs
	}, 0)

	if len(result) != 1 || result[0].path != "a.go" {
		t.Fatalf("expected only a.go, got %#v", result)
	}
}

func TestRankFilesRespectsLimit(t *testing.T) {
	files := []testFile{
		{path: "a.go", funcs: 1},
		{path: "b.go", funcs: 2},
		{path: "c.go", funcs: 3},
	}

	result := RankFiles(files, testPath, func(file testFile) int {
		return file.funcs
	}, 2)

	if len(result) != 2 || result[0].path != "c.go" {
		t.Fatalf("expected top two files starting with c.go, got %#v", result)
	}
}

func TestRankFilesBreaksTiesByPath(t *testing.T) {
	files := []testFile{
		{path: "z.go", funcs: 5},
		{path: "a.go", funcs: 5},
	}

	result := RankFiles(files, testPath, func(file testFile) int {
		return file.funcs
	}, 0)

	if len(result) != 2 || result[0].path != "a.go" {
		t.Fatalf("tie should be broken by path, got %#v", result)
	}
}

func TestUniqueRankedPathsDeduplicates(t *testing.T) {
	files := []testFile{
		{path: "a.go"},
		{path: "a.go"},
		{path: "b.go"},
	}

	result := UniqueRankedPaths(files, testPath, 0)

	if len(result) != 2 || result[0] != "a.go" || result[1] != "b.go" {
		t.Fatalf("expected [a.go b.go], got %#v", result)
	}
}

func TestUniqueRankedPathsRespectsLimit(t *testing.T) {
	files := []testFile{
		{path: "a.go"},
		{path: "b.go"},
		{path: "c.go"},
	}

	result := UniqueRankedPaths(files, testPath, 2)

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
}

func TestFindFilesExactPartialExactMatch(t *testing.T) {
	files := []testFile{
		{path: "src/main.cpp"},
		{path: "src/utils.h"},
	}

	result := FindFilesExactPartial(files, testPath, "main.cpp")

	if len(result) != 1 || result[0] != "src/main.cpp" {
		t.Fatalf("expected [src/main.cpp], got %#v", result)
	}
}

func TestFindFilesExactPartialPartialMatch(t *testing.T) {
	files := []testFile{
		{path: "src/main_window.cpp"},
		{path: "src/utils.h"},
	}

	result := FindFilesExactPartial(files, testPath, "main")

	if len(result) != 1 || result[0] != "src/main_window.cpp" {
		t.Fatalf("expected [src/main_window.cpp], got %#v", result)
	}
}

func TestFindFilesExactPartialEmptyPattern(t *testing.T) {
	files := []testFile{{path: "a.go"}}

	if result := FindFilesExactPartial(files, testPath, ""); result != nil {
		t.Fatalf("expected nil for empty pattern, got %#v", result)
	}
}

func TestFindFilesExactPartialNoMatch(t *testing.T) {
	files := []testFile{{path: "a.go"}}

	if result := FindFilesExactPartial(files, testPath, "xyz"); len(result) != 0 {
		t.Fatalf("expected empty result, got %#v", result)
	}
}

func TestFindFilesExactPartialFullPathExact(t *testing.T) {
	files := []testFile{
		{path: "src/main.cpp"},
		{path: "other/main.cpp"},
	}

	result := FindFilesExactPartial(files, testPath, "src/main.cpp")

	if len(result) != 1 || result[0] != "src/main.cpp" {
		t.Fatalf("expected [src/main.cpp], got %#v", result)
	}
}

func TestFindFilesExactPartialNormalizesPathSeparators(t *testing.T) {
	files := []testFile{
		{path: "src/main.cpp"},
		{path: `lib\worker.cpp`},
	}

	result := FindFilesExactPartial(files, testPath, `src\main.cpp`)
	if len(result) != 1 || result[0] != "src/main.cpp" {
		t.Fatalf("expected [src/main.cpp], got %#v", result)
	}

	result = FindFilesExactPartial(files, testPath, "worker.cpp")
	if len(result) != 1 || result[0] != `lib\worker.cpp` {
		t.Fatalf("expected [lib\\worker.cpp], got %#v", result)
	}
}

func TestFindFilesExactPartialNormalizesCurrentDirPrefix(t *testing.T) {
	files := []testFile{{path: "src/main.cpp"}}

	result := FindFilesExactPartial(files, testPath, `.\src\main.cpp`)
	if len(result) != 1 || result[0] != "src/main.cpp" {
		t.Fatalf("expected [src/main.cpp], got %#v", result)
	}
}

func TestFindFilesExactPartialSupportsStringPaths(t *testing.T) {
	files := []string{
		"src/main/java/com/example/App.java",
		`legacy\Worker.java`,
	}
	pathOf := func(path string) string { return path }

	if got := FindFilesExactPartial(files, pathOf, `src\`); len(got) != 1 {
		t.Fatalf("expected backslash path prefix to match one slash-normalized path, got %#v", got)
	}
	if got := FindFilesExactPartial(files, pathOf, "worker"); len(got) != 1 {
		t.Fatalf("expected basename match for backslash-indexed path, got %#v", got)
	}
}
