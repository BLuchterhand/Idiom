package plan

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Prompt edit scope", func() {
	It("does not treat allowed paths as required coverage", func() {
		prompt := `
## Allowed Edit Paths

- pkg/target.go
- pkg/optional.go

## Evidence

- pkg/evidence.go
`
		Expect(RequiredEditPathsFromPrompt(prompt)).To(BeEmpty())
		Expect(AllowedEditPathsFromPrompt(prompt)).To(And(
			ContainElement("pkg/target.go"),
			ContainElement("pkg/optional.go"),
			Not(ContainElement("pkg/evidence.go")),
		))
	})

	It("captures an inline required-path-only list", func() {
		prompt := "Implement a bounded harness fix. Required edit paths only: internal/workflows/planexecute/prompt.go, internal/workflows/planexecute/context_pack_test.go, internal/surfaces/tui/app/update_handlers.go, internal/surfaces/tui/app/lifecycle.go."
		expected := []string{
			"internal/workflows/planexecute/prompt.go",
			"internal/workflows/planexecute/context_pack_test.go",
			"internal/surfaces/tui/app/update_handlers.go",
			"internal/surfaces/tui/app/lifecycle.go",
		}
		Expect(RequiredEditPathsFromPrompt(prompt)).To(Equal(expected))
	})

	It("ignores slash prose after an inline required list", func() {
		prompt := "Implement the change. Required edit paths: `cmd/idiom/product_workflow.go`, `cmd/idiom/product_workflow_test.go`. Add focused tests for missing `--effort-root` and mutually exclusive/missing mode flags under JSON output."
		Expect(RequiredEditPathsFromPrompt(prompt)).To(Equal([]string{
			"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go",
		}))
	})

	It("rejects directory-like required obligations", func() {
		prompt := strings.Join([]string{
			"## Files To Modify",
			"",
			"- cmd/idiom",
			"- cmd/idiom/product_workflow.go",
			"- Makefile",
		}, "\n")
		Expect(RequiredEditPathsFromPrompt(prompt)).To(Equal([]string{"cmd/idiom/product_workflow.go", "Makefile"}))
	})

	It("combines structured and inline allowed scope without read-only evidence", func() {
		prompt := strings.Join([]string{
			`{"allowed_edit_paths": ["pkg/target.go"]}`,
			"Implement the behavior, but only edit pkg/feature.go and pkg/feature_test.go.",
			"Use pkg/evidence.go as read-only evidence.",
		}, "\n")
		Expect(RequiredEditPathsFromPrompt(prompt)).To(BeEmpty())
		Expect(AllowedEditPathsFromPrompt(prompt)).To(And(
			ContainElement("pkg/target.go"),
			ContainElement("pkg/feature.go"),
			ContainElement("pkg/feature_test.go"),
			Not(ContainElement("pkg/evidence.go")),
		))
	})
})
