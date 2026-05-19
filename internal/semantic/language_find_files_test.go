package semantic

import "testing"

func TestLanguageFindFilesNormalizePathSeparators(t *testing.T) {
	tests := []struct {
		name string
		find func(string) []string
	}{
		{
			name: "java",
			find: (&JavaIndex{Files: []JavaFile{
				{Path: "src/main/java/com/example/App.java"},
				{Path: `legacy\Worker.java`},
			}}).findFiles,
		},
		{
			name: "python",
			find: (&PythonIndex{Files: []PythonFile{
				{Path: "src/demo/core.py"},
				{Path: `legacy\worker.py`},
			}}).findFiles,
		},
		{
			name: "typescript",
			find: (&TsIndex{Files: []TsFile{
				{Path: "src/app.ts"},
				{Path: `legacy\worker.ts`},
			}}).findFiles,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.find(`src\`); len(got) != 1 {
				t.Fatalf("expected backslash path prefix to match one slash-normalized path, got %#v", got)
			}
			if got := tt.find("worker"); len(got) != 1 {
				t.Fatalf("expected basename match for backslash-indexed path, got %#v", got)
			}
		})
	}
}
