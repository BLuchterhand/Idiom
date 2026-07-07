package semantic_test

import (
	"testing"

	semanticcpp "idiom/internal/repository/semantic/cpp"
	semanticgo "idiom/internal/repository/semantic/golang"
	semanticjava "idiom/internal/repository/semantic/java"
	semanticpython "idiom/internal/repository/semantic/python"
	semanticrust "idiom/internal/repository/semantic/rust"
	semanticts "idiom/internal/repository/semantic/typescript"
)

func TestFileByPathNormalizesPathSeparators(t *testing.T) {
	goIndex := &semanticgo.GoIndex{Files: []semanticgo.GoFile{{Path: "internal/surfaces/tui/app/app.go"}}}
	if got := goIndex.FileByPath(` internal\surfaces\tui\app\app.go `); got == nil || got.Path != "internal/surfaces/tui/app/app.go" {
		t.Fatalf("Go FileByPath should normalize caller path separators, got %#v", got)
	}

	if got := goIndex.FileByPath(`.\internal\surfaces\tui\app\app.go`); got == nil || got.Path != "internal/surfaces/tui/app/app.go" {
		t.Fatalf("Go FileByPath should normalize caller current-directory prefix, got %#v", got)
	}

	rustIndex := &semanticrust.RustIndex{Files: []semanticrust.RustFile{{Path: "src/lib.rs"}}}
	if got := rustIndex.FileByPath(`src\lib.rs`); got == nil || got.Path != "src/lib.rs" {
		t.Fatalf("Rust FileByPath should normalize caller path separators, got %#v", got)
	}

	javaIndex := &semanticjava.JavaIndex{Files: []semanticjava.JavaFile{{Path: "src/main/java/App.java"}}}
	if got := javaIndex.FileByPath(`src\main\java\App.java`); got == nil || got.Path != "src/main/java/App.java" {
		t.Fatalf("Java FileByPath should normalize caller path separators, got %#v", got)
	}

	pythonIndex := &semanticpython.PythonIndex{Files: []semanticpython.PythonFile{{Path: "src/demo/core.py"}}}
	if got := pythonIndex.FileByPath(`src\demo\core.py`); got == nil || got.Path != "src/demo/core.py" {
		t.Fatalf("Python FileByPath should normalize caller path separators, got %#v", got)
	}

	tsIndex := &semanticts.TsIndex{Files: []semanticts.TsFile{{Path: "src/app.ts"}}}
	if got := tsIndex.FileByPath(`src\app.ts`); got == nil || got.Path != "src/app.ts" {
		t.Fatalf("TypeScript FileByPath should normalize caller path separators, got %#v", got)
	}

	cppIndex := &semanticcpp.CppIndex{Files: []semanticcpp.CppFile{{Path: "src/native/app.cpp"}}}
	if got := cppIndex.FileByPath(`src\native\app.cpp`); got == nil || got.Path != "src/native/app.cpp" {
		t.Fatalf("C++ FileByPath should normalize caller path separators, got %#v", got)
	}
}
