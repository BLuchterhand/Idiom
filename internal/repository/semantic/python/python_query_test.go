package python

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Python declaration queries", func() {
	It("normalizes class and async function declarations", func() {
		root := GinkgoT().TempDir()
		writePythonFixture(filepath.Join(root, "pyproject.toml"), `
[project]
name = "demo"
`)
		writePythonFixture(filepath.Join(root, "src", "demo", "core.py"), `
class Engine:
    def tick(self):
        return 1

async def load_engine():
    return Engine()
`)

		index, err := BuildPythonIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(index.FindSymbols("class Engine", 8)).NotTo(BeEmpty())
		Expect(index.FindSymbols("class Engine", 8)[0].Name).To(Equal("Engine"))
		Expect(index.FindSymbols("async def load_engine", 8)).NotTo(BeEmpty())
		Expect(index.FindSymbols("async def load_engine", 8)[0].Name).To(Equal("load_engine"))
	})
})
