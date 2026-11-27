package cpp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	. "github.com/onsi/ginkgo/v2"
)

// writeCppDirTree spreads one translation unit into each of dirs directories,
// enough that a walk with a spent deadline stops several times.
func writeCppDirTree(root string, dirs int) {
	t := GinkgoT()
	for i := range dirs {
		dir := filepath.Join(root, "core", fmt.Sprintf("part%03d", i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("namespace bt { void f%d() {} }\n", i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("unit%03d.cpp", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

var _ = Describe("C++ index walk across calls", func() {
	It("checkpoints a walk its deadline stops and resumes it without walking a finished directory again", func() {
		t := GinkgoT()
		root := t.TempDir()
		const dirs = 3*cppMinWalkedDirsPerCall + 5
		writeCppDirTree(root, dirs)

		spent, cancel := context.WithCancel(context.Background())
		cancel()

		var index *CppIndex
		walkStops := 0
		for calls := 0; ; calls++ {
			if calls > dirs {
				t.Fatalf("no finished build after %d calls", calls)
			}
			next, err := BuildCppIndexWithOptions(root, BuildOptions{Context: spent, Previous: index, Resume: index != nil})
			if next == nil {
				t.Fatalf("call %d returned no index: %v", calls, err)
			}

			var budget *BudgetError
			if errors.As(err, &budget) && budget.Phase == "walk" {
				walkStops++
				if next.WalkComplete || next.WalkPending == nil {
					t.Fatalf("call %d: a stopped walk must leave a checkpoint", calls)
				}
				found := 0
				if index != nil {
					found = len(index.Paths)
				}
				// Each call adds at most its quota of directories (one file each),
				// and never lists a directory a previous call finished.
				if added := len(next.Paths) - found; added < cppMinWalkedDirsPerCall-1 || added > cppMinWalkedDirsPerCall+1 {
					t.Fatalf("call %d found %d new files, want about %d", calls, added, cppMinWalkedDirsPerCall)
				}
			}

			index = next
			if err == nil {
				break
			}
			if !errors.As(err, &budget) {
				t.Fatalf("call %d: %v", calls, err)
			}
		}

		if walkStops < 3 {
			t.Fatalf("walk stopped %d times, want at least 3", walkStops)
		}
		if len(index.Paths) != dirs || len(slices.Compact(slices.Clone(index.Paths))) != dirs {
			t.Fatalf("resumed walk found %d paths, want %d distinct", len(index.Paths), dirs)
		}

		full, err := BuildCppIndex(root)
		if err != nil || !reflect.DeepEqual(full.Paths, index.Paths) || !reflect.DeepEqual(full.Files, index.Files) {
			t.Fatalf("resumed index differs from a full build: %v", err)
		}
	})

	It("stops a resumed walk at MaxFiles", func() {
		t := GinkgoT()
		root := t.TempDir()
		writeCppDirTree(root, 3*cppMinWalkedDirsPerCall)

		spent, cancel := context.WithCancel(context.Background())
		cancel()

		stopped, err := BuildCppIndexWithOptions(root, BuildOptions{Context: spent})
		var budget *BudgetError
		if !errors.As(err, &budget) || budget.Phase != "walk" || len(stopped.WalkPending) < 2 {
			t.Fatalf("want a walk checkpoint with at least two cursors: %v", err)
		}

		// One more file is the first file under the first cursor, so the walk
		// must stop there rather than carry on with the cursors after it.
		maxFiles := len(stopped.Paths) + 1
		resumed, err := BuildCppIndexWithOptions(root, BuildOptions{Previous: stopped, Resume: true, MaxFiles: maxFiles})
		if err != nil {
			t.Fatal(err)
		}
		if len(resumed.Paths) != maxFiles {
			t.Fatalf("resumed walk kept %d paths, want MaxFiles %d", len(resumed.Paths), maxFiles)
		}
	})
})
