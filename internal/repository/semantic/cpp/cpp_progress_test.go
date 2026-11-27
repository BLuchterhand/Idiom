package cpp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
)

// writeCppTree writes n small translation units that include a shared header
// and call each other, about the file count of the BenTech tree (1922 C/C++
// files on 2026-11-27).
func writeCppTree(root string, n int) {
	t := GinkgoT()
	if err := os.MkdirAll(filepath.Join(root, "core"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "core", "shared.h"), []byte("#pragma once\nnamespace bt { using Id = int; void shared(); }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := range n {
		body := fmt.Sprintf("#include \"shared.h\"\nnamespace bt {\nvoid f%d() { shared(); }\nvoid g%d() { f%d(); }\n}\n", i, i, i)
		if err := os.WriteFile(filepath.Join(root, "core", fmt.Sprintf("unit%04d.cpp", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

var _ = Describe("C++ index progress across calls", func() {
	It("finishes a cold BenTech-size tree in at most ceil(files / cppMinParsedPerCall) calls, never reparsing a finished file", func() {
		t := GinkgoT()
		root := t.TempDir()
		const units = 1922
		writeCppTree(root, units)
		total := units + 1

		// Every call arrives with its deadline already spent: the worst case.
		spent, cancel := context.WithCancel(context.Background())
		cancel()

		bound := (total + cppMinParsedPerCall - 1) / cppMinParsedPerCall
		var index *CppIndex
		calls := 0

		for ; calls <= bound; calls++ {
			resume := index != nil
			next, err := BuildCppIndexWithOptions(root, BuildOptions{Context: spent, Previous: index, Resume: resume})

			var budget *BudgetError
			if calls == 0 && errors.As(err, &budget) && budget.Phase == "walk" {
				t.Fatalf("a spent deadline must not stop the first walk from being retried: %v", err)
			}

			if next == nil {
				t.Fatalf("call %d returned no index: %v", calls, err)
			}

			before := 0
			if index != nil {
				before = len(index.Files)
			}

			if next.Timings.Reused != before {
				t.Fatalf("call %d reparsed finished files: reused %d of %d", calls, next.Timings.Reused, before)
			}

			if want := min(cppMinParsedPerCall, total-before); next.Timings.Parsed != want {
				t.Fatalf("call %d parsed %d files, want %d", calls, next.Timings.Parsed, want)
			}

			index = next
			if err == nil {
				break
			}

			if !errors.As(err, &budget) || len(next.Files) == 0 || len(next.Forward) == 0 {
				t.Fatalf("call %d: a stopped build must return its linked partial index: %v", calls, err)
			}
		}

		if calls+1 > bound {
			t.Fatalf("took %d calls, bound %d", calls+1, bound)
		}

		full, err := BuildCppIndex(root)
		if err != nil || !reflect.DeepEqual(full.Files, index.Files) || !reflect.DeepEqual(full.Aliases, index.Aliases) {
			t.Fatalf("resumed index differs from a full build: %v", err)
		}
	})

	It("reparses only the file whose content changed", func() {
		t := GinkgoT()
		root := t.TempDir()
		writeCppTree(root, 40)

		full, err := BuildCppIndex(root)
		if err != nil {
			t.Fatal(err)
		}

		changed := filepath.Join(root, "core", "unit0007.cpp")
		if err := os.WriteFile(changed, []byte("#include \"shared.h\"\nnamespace bt { void renamed7() {} }\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		next, err := BuildCppIndexWithOptions(root, BuildOptions{Previous: full, ChangedPaths: map[string]bool{"core/unit0007.cpp": true}})
		if err != nil {
			t.Fatal(err)
		}

		if next.Timings.Parsed != 1 || next.Timings.Reused != len(full.Files)-1 {
			t.Fatalf("parsed %d reused %d, want 1 and %d", next.Timings.Parsed, next.Timings.Reused, len(full.Files)-1)
		}

		file := next.FileByPath("core/unit0007.cpp")
		if file == nil || len(file.Symbols) == 0 || file.Symbols[len(file.Symbols)-1].Name != "renamed7" {
			t.Fatalf("changed file was not reparsed: %+v", file)
		}

		if old := full.FileByPath("core/unit0008.cpp"); !reflect.DeepEqual(*old, *next.FileByPath("core/unit0008.cpp")) {
			t.Fatal("an unchanged file's entry changed")
		}
	})
})
