package semantic

import "testing"

func TestFileByPathNormalizesPathSeparators(t *testing.T) {
	goIndex := &GoIndex{Files: []GoFile{{Path: "internal/surfaces/tui/app/app.go"}}}
	if got := goIndex.FileByPath(` internal\surfaces\tui\app\app.go `); got == nil || got.Path != "internal/surfaces/tui/app/app.go" {
		t.Fatalf("Go FileByPath should normalize caller path separators, got %#v", got)
	}

	if got := goIndex.FileByPath(`.\internal\surfaces\tui\app\app.go`); got == nil || got.Path != "internal/surfaces/tui/app/app.go" {
		t.Fatalf("Go FileByPath should normalize caller current-directory prefix, got %#v", got)
	}

	rustIndex := &RustIndex{Files: []RustFile{{Path: "src/lib.rs"}}}
	if got := rustIndex.FileByPath(`src\lib.rs`); got == nil || got.Path != "src/lib.rs" {
		t.Fatalf("Rust FileByPath should normalize caller path separators, got %#v", got)
	}

	javaIndex := &JavaIndex{Files: []JavaFile{{Path: "src/main/java/App.java"}}}
	if got := javaIndex.FileByPath(`src\main\java\App.java`); got == nil || got.Path != "src/main/java/App.java" {
		t.Fatalf("Java FileByPath should normalize caller path separators, got %#v", got)
	}

	pythonIndex := &PythonIndex{Files: []PythonFile{{Path: "src/demo/core.py"}}}
	if got := pythonIndex.FileByPath(`src\demo\core.py`); got == nil || got.Path != "src/demo/core.py" {
		t.Fatalf("Python FileByPath should normalize caller path separators, got %#v", got)
	}

	tsIndex := &TsIndex{Files: []TsFile{{Path: "src/app.ts"}}}
	if got := tsIndex.FileByPath(`src\app.ts`); got == nil || got.Path != "src/app.ts" {
		t.Fatalf("TypeScript FileByPath should normalize caller path separators, got %#v", got)
	}

	cppIndex := &CppIndex{Files: []CppFile{{Path: "src/native/app.cpp"}}}
	if got := cppIndex.FileByPath(`src\native\app.cpp`); got == nil || got.Path != "src/native/app.cpp" {
		t.Fatalf("C++ FileByPath should normalize caller path separators, got %#v", got)
	}
}
