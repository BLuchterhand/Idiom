package plan

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Required edit snippets", func() {
	It("skips current-state snippets and binds required edits to a single path", func() {
		prompt := `
# IMPLEMENTATION REQUEST

## Verified Current State

` + "```cpp" + `
TEST_CASE("old behavior") {}
` + "```" + `

## Required Edits

Add the logistics include:
` + "```cpp" + `
#include "core/constants/logistics.h"
` + "```" + `

Insert the demand-pressure regression:
` + "```cpp" + `
TEST_CASE("demand pressure caps production") {
	CHECK(DEMAND_PRESSURE_CAP > 0.0);
}
` + "```" + `
`
		snippets := RequiredEditSnippetsFromPrompt(prompt, []string{"native/tests/test_production_graph.cpp"})
		Expect(snippets).To(HaveLen(2))
		for _, snippet := range snippets {
			Expect(snippet.ID).NotTo(BeEmpty())
			Expect(snippet.Path).To(Equal("native/tests/test_production_graph.cpp"))
			Expect(snippet.Content).NotTo(ContainSubstring("old behavior"))
		}
		Expect(snippets[0].Content).To(ContainSubstring(`#include "core/constants/logistics.h"`))
		Expect(snippets[1].Content).To(ContainSubstring("demand pressure caps production"))
	})

	It("assigns snippets to their corresponding files", func() {
		prompt := `
## Required Edits

In ` + "`native/src/a.cpp`" + `, replace the source line.

old_text:

` + "```text" + `
int before();
` + "```" + `

new_text:

` + "```text" + `
int after();
` + "```" + `

In ` + "`native/tests/test_a.cpp`" + `, replace the test case.

old_text:

` + "```text" + `
TEST_CASE("before") {}
` + "```" + `

new_text:

` + "```text" + `
TEST_CASE("after") {}
` + "```" + `
`
		snippets := RequiredEditSnippetsFromPrompt(prompt, []string{"native/src/a.cpp", "native/tests/test_a.cpp"})
		Expect(snippets).To(HaveLen(2))
		contentByPath := map[string]string{}
		for _, snippet := range snippets {
			contentByPath[snippet.Path] = snippet.Content
		}
		Expect(contentByPath).To(HaveKeyWithValue("native/src/a.cpp", ContainSubstring("int after();")))
		Expect(contentByPath).To(HaveKeyWithValue("native/tests/test_a.cpp", ContainSubstring(`TEST_CASE("after")`)))
	})

	It("carries an old-text anchor without serializing it", func() {
		prompt := `
## Required Edits

In ` + "`native/src/a.cpp`" + `, replace the source line.

old_text:

` + "```text" + `
int before();
` + "```" + `

new_text:

` + "```text" + `
int after();
` + "```" + `
`
		snippets := RequiredEditSnippetsFromPrompt(prompt, []string{"native/src/a.cpp"})
		Expect(snippets).To(HaveLen(1))
		Expect(snippets[0].Path).To(Equal("native/src/a.cpp"))
		Expect(snippets[0].Content).NotTo(ContainSubstring("before"))
		Expect(snippets[0].Anchor).To(ContainSubstring("int before();"))

		data, err := json.Marshal(snippets[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(Or(ContainSubstring("Anchor"), ContainSubstring("before")))
	})

	It("normalizes backslash path labels", func() {
		prompt := `
## Required Edits

In ` + "`native\\src\\a.cpp`" + `, replace the source line.

new_text:

` + "```text" + `
int after();
` + "```" + `
`
		snippets := RequiredEditSnippetsFromPrompt(prompt, []string{"native/src/a.cpp"})
		Expect(snippets).To(HaveLen(1))
		Expect(snippets[0].Path).To(Equal("native/src/a.cpp"))
	})

	It("duplicates a shared-basename snippet across matching paths", func() {
		prompt := `
## Required Edits

Update the ` + "`a.h`" + ` and ` + "`a.cpp`" + ` comments.

old_text:

` + "```text" + `
// old shared comment
` + "```" + `

new_text:

` + "```text" + `
// new shared comment
` + "```" + `
`
		snippets := RequiredEditSnippetsFromPrompt(prompt, []string{"native/src/a.h", "native/src/a.cpp"})
		contentByPath := map[string]string{}
		for _, snippet := range snippets {
			contentByPath[snippet.Path] = snippet.Content
		}
		Expect(contentByPath).To(HaveKeyWithValue("native/src/a.h", ContainSubstring("// new shared comment")))
		Expect(contentByPath).To(HaveKeyWithValue("native/src/a.cpp", ContainSubstring("// new shared comment")))
	})
})
