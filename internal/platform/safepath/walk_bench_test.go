package safepath

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Synthetic tree sized like the workspace measured in G882: 4447 entries in
// 256 directories. 16 top directories of 15 subdirectories each give 256
// directories; 16 files in each gives 4096 files, 4352 entries in all.
const (
	benchTopDirs    = 16
	benchSubDirs    = 15
	benchFilesInDir = 16
)

func writeBenchTree(b *testing.B, root string) int {
	b.Helper()
	entries := 0
	writeFiles := func(dir string) {
		for i := range benchFilesInDir {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%02d.cpp", i)), []byte("int x;\n"), 0o600); err != nil {
				b.Fatal(err)
			}
			entries++
		}
	}
	for top := range benchTopDirs {
		topDir := filepath.Join(root, fmt.Sprintf("top%02d", top))
		if err := os.Mkdir(topDir, 0o700); err != nil {
			b.Fatal(err)
		}
		entries++
		writeFiles(topDir)
		for sub := range benchSubDirs {
			subDir := filepath.Join(topDir, fmt.Sprintf("sub%02d", sub))
			if err := os.Mkdir(subDir, 0o700); err != nil {
				b.Fatal(err)
			}
			entries++
			writeFiles(subDir)
		}
	}
	return entries
}

func benchWalk(b *testing.B, walk func(root string, fn fs.WalkDirFunc) error) {
	root := b.TempDir()
	want := writeBenchTree(b, root)
	b.ResetTimer()
	for b.Loop() {
		seen := -1 // the root itself
		err := walk(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			seen++
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
		if seen != want {
			b.Fatalf("walk saw %d entries, want %d", seen, want)
		}
	}
}

func BenchmarkWalkDirUnderRoot(b *testing.B) {
	benchWalk(b, func(root string, fn fs.WalkDirFunc) error {
		return WalkDirUnderRoot(root, root, "bench", fn)
	})
}

func BenchmarkFilepathWalkDir(b *testing.B) {
	benchWalk(b, filepath.WalkDir)
}
