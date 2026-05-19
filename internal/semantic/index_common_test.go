package semantic

import (
	"testing"
)

func TestRankFiles_SortsByScore(t *testing.T) {
	files := []CppFile{
		{Path: "a.go", Funcs: 1},
		{Path: "b.go", Funcs: 10},
		{Path: "c.go", Funcs: 5},
	}
	result := RankFiles(files, func(f CppFile) int {
		return f.Funcs
	}, 0)
	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result))
	}
	if result[0].Path != "b.go" {
		t.Errorf("first should be b.go, got %s", result[0].Path)
	}
	if result[1].Path != "c.go" {
		t.Errorf("second should be c.go, got %s", result[1].Path)
	}
	if result[2].Path != "a.go" {
		t.Errorf("third should be a.go, got %s", result[2].Path)
	}
}

func TestRankFiles_SkipsZeroAndNegative(t *testing.T) {
	files := []CppFile{
		{Path: "a.go", Funcs: 5},
		{Path: "b.go", Funcs: 0},
		{Path: "c.go", Funcs: -1},
	}
	result := RankFiles(files, func(f CppFile) int {
		return f.Funcs
	}, 0)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Path != "a.go" {
		t.Errorf("expected a.go, got %s", result[0].Path)
	}
}

func TestRankFiles_RespectsLimit(t *testing.T) {
	files := []CppFile{
		{Path: "a.go", Funcs: 1},
		{Path: "b.go", Funcs: 2},
		{Path: "c.go", Funcs: 3},
	}
	result := RankFiles(files, func(f CppFile) int {
		return f.Funcs
	}, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].Path != "c.go" {
		t.Errorf("first should be c.go, got %s", result[0].Path)
	}
}

func TestRankFiles_BreaksTiesByPath(t *testing.T) {
	files := []CppFile{
		{Path: "z.go", Funcs: 5},
		{Path: "a.go", Funcs: 5},
	}
	result := RankFiles(files, func(f CppFile) int {
		return f.Funcs
	}, 0)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].Path != "a.go" {
		t.Errorf("tie should be broken by path, got %s", result[0].Path)
	}
}

func TestRankFiles_WorksWithGoFiles(t *testing.T) {
	files := []GoFile{
		{Path: "main.go", Funcs: 1},
		{Path: "big.go", Funcs: 20},
	}
	result := RankFiles(files, func(f GoFile) int {
		return f.Funcs
	}, 0)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].Path != "big.go" {
		t.Errorf("first should be big.go, got %s", result[0].Path)
	}
}

func TestUniqueRankedPaths_Deduplicates(t *testing.T) {
	files := []CppFile{
		{Path: "a.go"},
		{Path: "a.go"},
		{Path: "b.go"},
	}
	result := UniqueRankedPaths(files, 0)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(result), result)
	}
	if result[0] != "a.go" || result[1] != "b.go" {
		t.Errorf("expected [a.go b.go], got %v", result)
	}
}

func TestUniqueRankedPaths_RespectsLimit(t *testing.T) {
	files := []CppFile{
		{Path: "a.go"},
		{Path: "b.go"},
		{Path: "c.go"},
	}
	result := UniqueRankedPaths(files, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
}

func TestFindFilesExactPartial_ExactMatch(t *testing.T) {
	files := []CppFile{
		{Path: "src/main.cpp"},
		{Path: "src/utils.h"},
	}
	result := FindFilesExactPartial(files, "main.cpp")
	if len(result) != 1 || result[0] != "src/main.cpp" {
		t.Errorf("expected [src/main.cpp], got %v", result)
	}
}

func TestFindFilesExactPartial_PartialMatch(t *testing.T) {
	files := []CppFile{
		{Path: "src/main_window.cpp"},
		{Path: "src/utils.h"},
	}
	result := FindFilesExactPartial(files, "main")
	if len(result) != 1 || result[0] != "src/main_window.cpp" {
		t.Errorf("expected [src/main_window.cpp], got %v", result)
	}
}

func TestFindFilesExactPartial_EmptyPattern(t *testing.T) {
	files := []CppFile{{Path: "a.go"}}
	result := FindFilesExactPartial(files, "")
	if result != nil {
		t.Errorf("expected nil for empty pattern, got %v", result)
	}
}

func TestFindFilesExactPartial_NoMatch(t *testing.T) {
	files := []CppFile{{Path: "a.go"}}
	result := FindFilesExactPartial(files, "xyz")
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestFindFilesExactPartial_FullPathExact(t *testing.T) {
	files := []CppFile{
		{Path: "src/main.cpp"},
		{Path: "other/main.cpp"},
	}
	result := FindFilesExactPartial(files, "src/main.cpp")
	if len(result) != 1 || result[0] != "src/main.cpp" {
		t.Errorf("expected [src/main.cpp], got %v", result)
	}
}

func TestFindFilesExactPartial_NormalizesPathSeparators(t *testing.T) {
	files := []CppFile{
		{Path: "src/main.cpp"},
		{Path: `lib\worker.cpp`},
	}

	result := FindFilesExactPartial(files, `src\main.cpp`)
	if len(result) != 1 || result[0] != "src/main.cpp" {
		t.Errorf("expected [src/main.cpp], got %v", result)
	}

	result = FindFilesExactPartial(files, "worker.cpp")
	if len(result) != 1 || result[0] != `lib\worker.cpp` {
		t.Errorf("expected [lib\\worker.cpp], got %v", result)
	}
}
