package indexsupport

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type testFile struct {
	path  string
	funcs int
}

func testPath(file testFile) string {
	return file.path
}

var _ = Describe("Semantic index result ordering", func() {
	Describe("RankFiles", func() {
		It("sorts positive scores from highest to lowest", func() {
			files := []testFile{{path: "a.go", funcs: 1}, {path: "b.go", funcs: 10}, {path: "c.go", funcs: 5}}
			result := RankFiles(files, testPath, func(file testFile) int { return file.funcs }, 0)
			Expect(result).To(Equal([]testFile{{path: "b.go", funcs: 10}, {path: "c.go", funcs: 5}, {path: "a.go", funcs: 1}}))
		})

		It("omits zero and negative scores", func() {
			files := []testFile{{path: "a.go", funcs: 5}, {path: "b.go"}, {path: "c.go", funcs: -1}}
			result := RankFiles(files, testPath, func(file testFile) int { return file.funcs }, 0)
			Expect(result).To(Equal([]testFile{{path: "a.go", funcs: 5}}))
		})

		It("honors the result limit", func() {
			files := []testFile{{path: "a.go", funcs: 1}, {path: "b.go", funcs: 2}, {path: "c.go", funcs: 3}}
			result := RankFiles(files, testPath, func(file testFile) int { return file.funcs }, 2)
			Expect(result).To(Equal([]testFile{{path: "c.go", funcs: 3}, {path: "b.go", funcs: 2}}))
		})

		It("breaks score ties by path", func() {
			files := []testFile{{path: "z.go", funcs: 5}, {path: "a.go", funcs: 5}}
			result := RankFiles(files, testPath, func(file testFile) int { return file.funcs }, 0)
			Expect(result).To(Equal([]testFile{{path: "a.go", funcs: 5}, {path: "z.go", funcs: 5}}))
		})
	})

	Describe("UniqueRankedPaths", func() {
		It("deduplicates paths while preserving rank order", func() {
			files := []testFile{{path: "a.go"}, {path: "a.go"}, {path: "b.go"}}
			Expect(UniqueRankedPaths(files, testPath, 0)).To(Equal([]string{"a.go", "b.go"}))
		})

		It("honors the result limit", func() {
			files := []testFile{{path: "a.go"}, {path: "b.go"}, {path: "c.go"}}
			Expect(UniqueRankedPaths(files, testPath, 2)).To(Equal([]string{"a.go", "b.go"}))
		})
	})

	Describe("FindFilesExactPartial", func() {
		DescribeTable("finds exact and partial paths",
			func(files []testFile, pattern string, expected []string) {
				Expect(FindFilesExactPartial(files, testPath, pattern)).To(Equal(expected))
			},
			Entry("exact basename", []testFile{{path: "src/main.cpp"}, {path: "src/utils.h"}}, "main.cpp", []string{"src/main.cpp"}),
			Entry("partial basename", []testFile{{path: "src/main_window.cpp"}, {path: "src/utils.h"}}, "main", []string{"src/main_window.cpp"}),
			Entry("exact full path", []testFile{{path: "src/main.cpp"}, {path: "other/main.cpp"}}, "src/main.cpp", []string{"src/main.cpp"}),
			Entry("normalized path separators", []testFile{{path: "src/main.cpp"}, {path: `lib\worker.cpp`}}, `src\main.cpp`, []string{"src/main.cpp"}),
			Entry("normalized current-directory prefix", []testFile{{path: "src/main.cpp"}}, `.\src\main.cpp`, []string{"src/main.cpp"}),
		)

		It("returns nil for an empty pattern", func() {
			Expect(FindFilesExactPartial([]testFile{{path: "a.go"}}, testPath, "")).To(BeNil())
		})

		It("returns no results when nothing matches", func() {
			Expect(FindFilesExactPartial([]testFile{{path: "a.go"}}, testPath, "xyz")).To(BeEmpty())
		})

		It("matches a basename stored with backslash separators", func() {
			files := []testFile{{path: "src/main.cpp"}, {path: `lib\worker.cpp`}}
			Expect(FindFilesExactPartial(files, testPath, "worker.cpp")).To(Equal([]string{`lib\worker.cpp`}))
		})

		It("supports indexes whose values are path strings", func() {
			files := []string{"src/main/java/com/example/App.java", `legacy\Worker.java`}
			pathOf := func(path string) string { return path }
			Expect(FindFilesExactPartial(files, pathOf, `src\`)).To(HaveLen(1))
			Expect(FindFilesExactPartial(files, pathOf, "worker")).To(HaveLen(1))
		})
	})
})
