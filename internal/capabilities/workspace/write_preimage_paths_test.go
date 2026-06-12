package workspace

import (
	"slices"
	"testing"
)

func TestWritePreimagePathsCoversEveryWorkspaceWriteTool(t *testing.T) {
	for _, tool := range WorkspaceWriteToolNames() {
		if _, ok, _ := WritePreimagePaths(ToolRequest{Tool: tool}); !ok {
			t.Fatalf("workspace write tool %q is missing a preimage path classification", tool)
		}
	}
}

func TestWritePreimagePathsExtractsDirectWriteTargets(t *testing.T) {
	for _, tool := range []string{ToolNameEditFile, ToolNameWriteFile, ToolNameAppendFile, ToolNameDelegateEdit} {
		paths, ok, err := WritePreimagePaths(ToolRequest{Tool: tool, Path: "README.md"})
		if err != nil || !ok {
			t.Fatalf("%s extraction failed: ok=%v err=%v", tool, ok, err)
		}

		if !slices.Equal(paths, []string{"README.md"}) {
			t.Fatalf("%s paths = %#v", tool, paths)
		}
	}
}

func TestWritePreimagePathsExtractsApplyPatchTargets(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: a.txt\n" +
		"@@\n" +
		"-before\n" +
		"+after\n" +
		"*** Add File: b.txt\n" +
		"+new\n" +
		"*** End Patch"
	paths, ok, err := WritePreimagePaths(ToolRequest{Tool: ToolNameApplyPatch, Content: patch})

	if err != nil || !ok {
		t.Fatalf("apply_patch extraction failed: ok=%v err=%v", ok, err)
	}

	if !slices.Equal(paths, []string{"a.txt", "b.txt"}) {
		t.Fatalf("apply_patch paths = %#v", paths)
	}
}

func TestWritePreimagePathsExtractsGoMoveTargets(t *testing.T) {
	paths, ok, err := WritePreimagePaths(ToolRequest{
		Tool:        ToolNameGoMoveDeclarations,
		Path:        "source.go",
		Destination: "dest.go",
		Moves: []GoDeclarationMove{
			{Destination: "other.go"},
			{Destination: "dest.go"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("go_move_declarations extraction failed: ok=%v err=%v", ok, err)
	}

	if !slices.Equal(paths, []string{"source.go", "dest.go", "other.go"}) {
		t.Fatalf("go_move_declarations paths = %#v", paths)
	}
}

func TestWritePreimagePathsAllowsWriteToolsWithoutFilePreimage(t *testing.T) {
	paths, ok, err := WritePreimagePaths(ToolRequest{Tool: ToolNameMkdir, Path: "new-dir"})
	if err != nil || !ok {
		t.Fatalf("mkdir classification failed: ok=%v err=%v", ok, err)
	}

	if len(paths) != 0 {
		t.Fatalf("mkdir should not produce file preimage paths, got %#v", paths)
	}
}
