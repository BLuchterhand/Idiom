package languagetools

import (
	semanticcpp "idiom/internal/repository/semantic/cpp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("C++ dead-symbol verdict", func() {
	resolution := semanticcpp.CppResolution{
		Owner: "bentech::is_valid",
		Definitions: []semanticcpp.CppSymbolMatch{
			{Path: "core/include/bentech/handle.h", Line: 11, StartLine: 11, EndLine: 11},
			{Path: "core/src/sim/handle.cpp", Line: 5, StartLine: 5, EndLine: 7},
		},
	}

	It("never reports dead when no reference backend produced evidence", func() {
		verdict := cppDeadSymbolVerdict(ToolResult{Warnings: []string{"clangd references unavailable"}}, cppReferencesNoEvidence, resolution)

		Expect(verdict.Error).NotTo(BeEmpty())
		Expect(verdict.Failure).To(HaveOccurred())
		Expect(verdict.Summary).To(ContainSubstring("Cannot decide"))
		Expect(verdict.Summary).NotTo(ContainSubstring("dead."))
		Expect(verdict.Warnings).To(ConsistOf("clangd references unavailable"))
	})

	It("reports dead when only the symbol's own declarations and body reference it", func() {
		references := ToolResult{Output: "f1 core/include/bentech/handle.h:11:bool is_valid(Handle h);\n" +
			"f2 core/src/sim/handle.cpp:5:bool is_valid(Handle h) {\n" +
			"f2 core/src/sim/handle.cpp:6:    return is_valid(h);\n" +
			"context:\n4:\n5:bool is_valid(Handle h) {\n6:    return is_valid(h);"}

		verdict := cppDeadSymbolVerdict(references, cppReferencesByTextSearch, resolution)

		Expect(verdict.Error).To(BeEmpty())
		Expect(verdict.Summary).To(ContainSubstring("likely dead"))
		Expect(verdict.Summary).To(ContainSubstring("text search"))
	})

	It("counts uses outside the definitions, including clangd's handle-prefixed lines", func() {
		references := ToolResult{Output: "f1 core/include/bentech/handle.h core/include/bentech/handle.h:11:bool is_valid(Handle h);\n" +
			"f2 core/src/sim/handle.cpp core/src/sim/handle.cpp:5:bool is_valid(Handle h) {\n" +
			"f3 core/src/sim/tick.cpp core/src/sim/tick.cpp:3:return is_valid(h) ? 1 : 0;\n" +
			"f4 bindings/src/sim_binding.cpp bindings/src/sim_binding.cpp:2:return bentech::is_valid(h);"}

		verdict := cppDeadSymbolVerdict(references, cppReferencesByClangd, resolution)

		Expect(verdict.Error).To(BeEmpty())
		Expect(verdict.Summary).To(ContainSubstring("Found 2 reference(s)"))
		Expect(verdict.Summary).To(ContainSubstring("via clangd"))
		Expect(verdict.Output).To(ContainSubstring("tick.cpp"))
		Expect(verdict.Output).NotTo(ContainSubstring("handle.h"))
	})

	It("matches Windows drive-qualified reference paths against definitions", func() {
		windows := semanticcpp.CppResolution{
			Owner:       "bentech::is_valid",
			Definitions: []semanticcpp.CppSymbolMatch{{Path: "C:/repo/core/src/handle.cpp", Line: 5, StartLine: 5, EndLine: 7}},
		}
		references := ToolResult{Output: "f1 C:/repo/core/src/handle.cpp:5:bool is_valid(Handle h) {\nf2 C:/repo/core/src/tick.cpp:3:return is_valid(h);"}

		verdict := cppDeadSymbolVerdict(references, cppReferencesByClangd, windows)

		Expect(verdict.Summary).To(ContainSubstring("Found 1 reference(s)"))
		Expect(verdict.Output).To(ContainSubstring("tick.cpp"))
	})
})
