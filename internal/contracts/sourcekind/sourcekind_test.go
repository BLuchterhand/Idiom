package sourcekind

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Source-kind policy", func() {
	ginkgo.DescribeTable("identifies source files covered by sanity checks",
		func(path string) {
			gomega.Expect(CoversSourceSanity(path)).To(gomega.BeTrue())
		},
		ginkgo.Entry("Go", "main.go"),
		ginkgo.Entry("C++", "native/sim.cpp"),
		ginkgo.Entry("TypeScript declaration", "types/index.d.ts"),
		ginkgo.Entry("Godot", "scripts/demo.gd"),
		ginkgo.Entry("TSX", "src/app.tsx"),
	)

	ginkgo.DescribeTable("excludes non-source files from sanity checks",
		func(path string) {
			gomega.Expect(CoversSourceSanity(path)).To(gomega.BeFalse())
		},
		ginkgo.Entry("Markdown", "README.md"),
		ginkgo.Entry("YAML", "config.yaml"),
		ginkgo.Entry("empty", ""),
	)

	ginkgo.DescribeTable("requires concrete diagnostics only for C-family source",
		func(path string, expected bool) {
			gomega.Expect(RequiresConcreteSourceSanityDiagnostics(path)).To(gomega.Equal(expected))
		},
		ginkgo.Entry("C", "native/sim.c", true),
		ginkgo.Entry("CC", "native/sim.cc", true),
		ginkgo.Entry("CPP", "native/sim.cpp", true),
		ginkgo.Entry("CXX", "native/sim.cxx", true),
		ginkgo.Entry("H", "native/sim.h", true),
		ginkgo.Entry("HH", "native/sim.hh", true),
		ginkgo.Entry("HPP", "native/sim.hpp", true),
		ginkgo.Entry("HXX", "native/sim.hxx", true),
		ginkgo.Entry("IPP", "native/sim.ipp", true),
		ginkgo.Entry("INL", "native/sim.inl", true),
		ginkgo.Entry("TPP", "native/sim.tpp", true),
		ginkgo.Entry("normalizes case and whitespace", "  NATIVE/SIM.CPP  ", true),
		ginkgo.Entry("Go", "main.go", false),
		ginkgo.Entry("TypeScript", "src/app.ts", false),
		ginkgo.Entry("Rust", "src/lib.rs", false),
		ginkgo.Entry("empty", "", false),
	)
})
