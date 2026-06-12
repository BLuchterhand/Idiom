package semantic

import "testing"

func TestLanguageFindFilesNormalizePathSeparators(t *testing.T) {
	tests := []struct {
		name  string
		files []string
	}{
		{
			name: "java",
			files: []string{
				"src/main/java/com/example/App.java",
				`legacy\Worker.java`,
			},
		},
		{
			name: "python",
			files: []string{
				"src/demo/core.py",
				`legacy\worker.py`,
			},
		},
		{
			name: "typescript",
			files: []string{
				"src/app.ts",
				`legacy\worker.ts`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FindFilesExactPartial(tt.files, `src\`); len(got) != 1 {
				t.Fatalf("expected backslash path prefix to match one slash-normalized path, got %#v", got)
			}

			if got := FindFilesExactPartial(tt.files, "worker"); len(got) != 1 {
				t.Fatalf("expected basename match for backslash-indexed path, got %#v", got)
			}
		})
	}
}

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
