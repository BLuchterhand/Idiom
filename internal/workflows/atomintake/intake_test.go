package atomintake

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	atomintakecontract "idiom/internal/contracts/atomintake"
)

var _ = ginkgo.Describe("Atom intake", func() {
	ginkgo.It("dispatches a planned atom with existing scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
	})

	ginkgo.It("canonicalizes absolute in-root scope to repository-relative paths", func() {
		root := ginkgo.GinkgoT().TempDir()
		absPath := filepath.Join(root, "src", "foo.go")
		writeAtomFile(root, "src/foo.go", "package src\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `"+absPath+"`\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
		gomega.Expect(report.ScopePaths).To(gomega.Equal([]string{"src/foo.go"}))
		gomega.Expect(report.Existing).To(gomega.Equal([]string{"src/foo.go"}))
		for _, path := range append(append([]string(nil), report.ScopePaths...), report.Existing...) {
			gomega.Expect(filepath.IsAbs(path)).To(gomega.BeFalse(), "path: %s", path)
		}
	})

	ginkgo.It("classifies an accepted atom as complete before considering missing scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		report, code, stderr := runAtomIntakeJSONForTest(root, writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: accepted\n"))
		expectIntakeState(report, code, stderr, atomintakecontract.StateComplete, false)
	})

	ginkgo.It("refuses a source-satisfied atom", func() {
		root := ginkgo.GinkgoT().TempDir()
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: source-satisfied - do not dispatch\n\n## Scope\n\n- `foo.go`\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateSourceSatisfied, false)
	})

	ginkgo.DescribeTable("classifying terminal tracker statuses",
		func(status string, terminal bool) {
			gotStatus, got := TerminalStatus(status)
			gomega.Expect(got).To(gomega.Equal(terminal))
			if terminal {
				gomega.Expect(gotStatus).To(gomega.Equal(status))
			}
		},
		ginkgo.Entry("source-satisfied", "source-satisfied - do not dispatch", true),
		ginkgo.Entry("completed", "completed", true),
		ginkgo.Entry("closed", "closed", true),
		ginkgo.Entry("accepted", "accepted", true),
		ginkgo.Entry("pending", "pending", false),
		ginkgo.Entry("planned", "planned", false),
	)

	ginkgo.It("preserves literal event evidence when source already covers the scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := "internal/surfaces/tui/app/writer_route.go"
		writeAtomFile(root, path, strings.Join([]string{
			"package app",
			"",
			"func emitWriterRouteDecision() {",
			"\tfields[\"type\"] = \"writer_route_decision\"",
			"\tconst namespace = \"idiom.writer_route.\"",
			"}",
			"",
		}, "\n"))
		report, ok := EvaluateSourceSatisfiedScope(SourceSatisfiedScopeOptions{
			ProjectRoot: root,
			SpecPath:    ".idiom/atoms/design/atom-007.md",
			SpecBody:    "# Atom\n",
			ScopePath:   path,
			ScopeText:   "Build and emit TUI route decision after `idiom.writer_route_decision.v1` without editing `.idiom/atoms/design/atom-007.md`.",
		})
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(report.Path).To(gomega.Equal(path))
		gomega.Expect(strings.Join(report.Evidence, "\n")).To(gomega.And(
			gomega.ContainSubstring("writer_route_decision"),
			gomega.ContainSubstring("idiom.writer_route."),
		))
	})

	ginkgo.It("detects when every required edit snippet is already present", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n\nfunc bounded() int {\n\treturn 42\n}\n")
		writeAtomFile(root, "foo_test.go", "package main\n\nimport \"testing\"\n\nfunc TestBounded(t *testing.T) {\n\tif bounded() != 42 {\n\t\tt.Fatal(\"wrong value\")\n\t}\n}\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add helper. |\n| `foo_test.go` | Add coverage. |\n\n## Required Edits\n\nIn `foo.go`, add:\n\n```go\nfunc bounded() int {\n    return 42\n}\n```\n\nIn `foo_test.go`, add:\n\n```go\nfunc TestBounded(t *testing.T) {\n    if bounded() != 42 {\n        t.Fatal(\"wrong value\")\n    }\n}\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateSourceSatisfied, false)
		gomega.Expect(reportReasonContains(report, "all required edit snippets already appear")).To(gomega.BeTrue())
	})

	ginkgo.It("applies a shared required-edit snippet to every named path", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "a.h", "// new shared comment\n")
		writeAtomFile(root, "a.cpp", "// new shared comment\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `a.h` | Update header comment. |\n| `a.cpp` | Update implementation comment. |\n\n## Required Edits\n\nUpdate the `a.h` and `a.cpp` comments.\n\nold_text:\n\n```text\n// old shared comment\n```\n\nnew_text:\n\n```text\n// new shared comment\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateSourceSatisfied, false)
	})

	ginkgo.It("blocks a partially applied set of required snippets", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n\nfunc bounded() int {\n\treturn 42\n}\n")
		writeAtomFile(root, "foo_test.go", "package main\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add helper. |\n| `foo_test.go` | Add coverage. |\n\n## Required Edits\n\nIn `foo.go`, add:\n\n```go\nfunc bounded() int {\n    return 42\n}\n```\n\nIn `foo_test.go`, add:\n\n```go\nfunc TestBounded(t *testing.T) {\n    if bounded() != 42 {\n        t.Fatal(\"wrong value\")\n    }\n}\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateStaleNeedsRebase, false)
	})

	ginkgo.It("keeps an atom dispatchable when no required snippet matches", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add helper. |\n\n## Required Edits\n\nIn `foo.go`, add:\n\n```go\nfunc bounded() int {\n    return 42\n}\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
	})

	ginkgo.It("blocks a superseded required-edit snippet", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", `package main

func buildCandidate() Candidate {
	return Candidate{
		Owner: owner,
		Slot: slot,
		Quality: quality,
		Ground: ownerIsGround,
	}
}
`)
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Build candidate through the bounded helper. |\n\n## Required Edits\n\nIn `foo.go`, replace the candidate block with:\n\n```go\nfunc buildCandidate() Candidate {\n    return Candidate{\n        Owner: owner,\n        Slot: slot,\n        Quality: quality,\n        Ground: false,\n    }\n}\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateStaleNeedsRebase, false)
		gomega.Expect(reportReasonContains(report, "near-matches required_edit_1")).To(gomega.BeTrue())
	})

	ginkgo.It("keeps an anchored insertion dispatchable while its old text matches", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n\nimport (\n\t\"a\"\n\t\"c\"\n)\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add the missing import. |\n\n## Required Edits\n\nIn `foo.go`, replace the import block.\n\nold_text:\n\n```go\nimport (\n    \"a\"\n    \"c\"\n)\n```\n\nnew_text:\n\n```go\nimport (\n    \"a\"\n    \"b\"\n    \"c\"\n)\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
		gomega.Expect(reportReasonContains(report, "near-matches required_edit_1")).To(gomega.BeFalse())
	})

	ginkgo.It("keeps an anchored deletion dispatchable when the new text is an ordered subset", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n\nimport (\n\t\"a\"\n\t\"b\"\n\t\"c\"\n)\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Remove the obsolete import. |\n\n## Required Edits\n\nIn `foo.go`, replace the import block.\n\nold_text:\n\n```go\nimport (\n    \"a\"\n    \"b\"\n    \"c\"\n)\n```\n\nnew_text:\n\n```go\nimport (\n    \"a\"\n    \"c\"\n)\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
		gomega.Expect(reportReasonContains(report, "contains required_edit_1")).To(gomega.BeFalse())
		gomega.Expect(reportReasonContains(report, "near-matches required_edit_1")).To(gomega.BeFalse())
	})

	ginkgo.It("blocks an anchored near-match after the old text drifts", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", `package main

func buildCandidate() Candidate {
	return Candidate{
		Owner: owner,
		Slot: slot,
		Quality: quality,
		Ground: ownerIsGround,
	}
}
`)
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Build candidate through the bounded helper. |\n\n## Required Edits\n\nIn `foo.go`, replace the candidate block.\n\nold_text:\n\n```go\nfunc buildCandidate() Candidate {\n    return Candidate{\n        Owner: owner,\n        Slot: slot,\n        Quality: quality,\n        Ground: true,\n    }\n}\n```\n\nnew_text:\n\n```go\nfunc buildCandidate() Candidate {\n    return Candidate{\n        Owner: owner,\n        Slot: slot,\n        Quality: quality,\n        Ground: false,\n    }\n}\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateStaleNeedsRebase, false)
		gomega.Expect(reportReasonContains(report, "near-matches required_edit_1")).To(gomega.BeTrue())
	})

	ginkgo.It("does not treat a current-state fence as a required edit", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n\nfunc existing() {}\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n\n## Required Edits\n\nCurrent:\n\n```go\nfunc existing() {}\n```\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
	})

	ginkgo.It("detects source-satisfied current-state anchored scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "internal/contracts/writerroute/decision_test.go", strings.Join([]string{
			"package writerroute",
			"",
			"func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {",
			"\tt.Fatal(\"covers normalization stable id\")",
			"}",
			"",
			"func TestWriterRouteDecision_JSONEventFieldMapping(t *testing.T) {",
			"\tt.Fatal(\"covers JSON event fields\")",
			"}",
			"",
		}, "\n"))
		specPath := writeAtomSpec(root, "spec.md", strings.Join([]string{
			"# Atom", "", "Status: pending", "", "## Files To Modify", "", "| File | Change |", "|---|---|",
			"| internal/contracts/writerroute/decision_test.go | Unit tests for normalization, stable ID, JSON/event fields |", "",
			"## Verified Current State", "", "- Host current-state anchors captured before dispatch:",
			"  - `internal/contracts/writerroute/decision_test.go:3`: `func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {`", "",
			"## Required Behavior", "", "Implement only this atom's bounded slice for `internal/contracts/writerroute/decision_test.go`.", "",
		}, "\n"))
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateSourceSatisfied, false)
		gomega.Expect(reportReasonContains(report, "current source covers the atom's current-state anchored scope")).To(gomega.BeTrue())
	})

	ginkgo.It("keeps uncovered current-state anchors dispatchable", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n\nconst A = \"old\"\n")
		specPath := writeAtomSpec(root, "spec.md", strings.Join([]string{
			"# Atom", "", "Status: pending", "", "## Files To Modify", "", "| File | Change |", "|---|---|",
			"| foo.go | Replace fixture value with new behavior |", "", "## Verified Current State", "",
			"- Host current-state anchors captured before dispatch:", "  - `foo.go:3`: `const A = \"old\"`", "",
		}, "\n"))
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
	})

	ginkgo.It("source-satisfies optional scope only when its current-state anchor is live", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "dispatch.go", "package main\n\ntype dispatchPlanDocument struct {\n\tPath string\n}\n")
		specPath := writeAtomSpec(root, "spec.md", strings.Join([]string{
			"# Atom", "", "Status: pending", "", "## Files To Modify", "", "| File | Change |", "|---|---|",
			"| dispatch.go | If needed, pass or annotate dispatch route without changing behavior |", "", "## Verified Current State", "",
			"- Host current-state anchors captured before dispatch:", "  - `dispatch.go:3`: `type dispatchPlanDocument struct {`", "",
		}, "\n"))
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateSourceSatisfied, false)
		gomega.Expect(reportReasonContains(report, "optional current-state anchor remains current")).To(gomega.BeTrue())
	})

	ginkgo.It("does not source-satisfy optional scope from weak body terms when the anchor is stale", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "dispatch.go", "package main\n\ntype dispatchPlanDocument struct {\n\tPath string\n}\n")
		specPath := writeAtomSpec(root, "spec.md", strings.Join([]string{
			"# Atom", "", "Status: pending", "", "## Files To Modify", "", "| File | Change |", "|---|---|",
			"| dispatch.go | If needed, pass or annotate dispatch route without changing behavior |", "", "## Verified Current State", "",
			"- Host current-state anchors captured before dispatch:", "  - `dispatch.go:3`: `func staleAnchor() {}`", "",
		}, "\n"))
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
	})

	ginkgo.It("blocks a plan with no scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		report, code, stderr := runAtomIntakeJSONForTest(root, writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Acceptance\n\n- tests pass\n"))
		expectIntakeState(report, code, stderr, atomintakecontract.StateMissingScope, false)
	})

	ginkgo.It("uses expected-file scope while ignoring dotted-symbol non-goals", func() {
		root := ginkgo.GinkgoT().TempDir()
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: implementation handoff\n\n## Expected File Scope\n\n| File | Purpose |\n|---|---|\n| `internal/contracts/writerroute/decision.go` | Domain type. |\n| `internal/surfaces/tui/app/prompt_routing.go` or new `internal/surfaces/tui/app/writer_route.go` | TUI event wiring. |\n\n## Non-Goals\n\n- Do not add `idiom.code_draft.v1` implementation.\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
		gomega.Expect(report.ScopePaths).To(gomega.Equal([]string{
			"internal/contracts/writerroute/decision.go",
			"internal/surfaces/tui/app/prompt_routing.go",
			"internal/surfaces/tui/app/writer_route.go",
		}))
		gomega.Expect(report.Invalid).To(gomega.BeEmpty())
	})

	ginkgo.It("blocks scope that escapes the project root", func() {
		root := ginkgo.GinkgoT().TempDir()
		report, code, stderr := runAtomIntakeJSONForTest(root, writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `../outside.go`\n"))
		expectIntakeState(report, code, stderr, atomintakecontract.StateInvalidScope, false)
		gomega.Expect(report.Invalid).To(gomega.Equal([]string{"../outside.go"}))
		gomega.Expect(report.InvalidReasons).To(gomega.HaveKeyWithValue("../outside.go", atomintakecontract.InvalidScopeOutsideProjectRoot))
		gomega.Expect(reportReasonContains(report, "outside the project root")).To(gomega.BeTrue())
	})

	ginkgo.It("rejects an ambiguous missing bare-file shorthand", func() {
		root := ginkgo.GinkgoT().TempDir()
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `test_codex_generation.cpp` | Shorthand basename from prose should not authorize a root write. |\n| `native/tests/test_codex_generation.cpp` | Concrete repo-relative new file. |\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateInvalidScope, false)
		gomega.Expect(report.Invalid).To(gomega.Equal([]string{"test_codex_generation.cpp"}))
		gomega.Expect(report.InvalidReasons).To(gomega.HaveKeyWithValue("test_codex_generation.cpp", atomintakecontract.InvalidScopeAmbiguousBareShorthand))
		gomega.Expect(reportReasonContains(report, "outside the project root")).To(gomega.BeFalse())
		gomega.Expect(reportReasonContains(report, "ambiguous bare shorthand")).To(gomega.BeTrue())
		gomega.Expect(report.Missing).To(gomega.Equal([]string{"native/tests/test_codex_generation.cpp"}))
	})

	ginkgo.It("reports each reason in a mixed invalid scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		report, code, stderr := runAtomIntakeJSONForTest(root, writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `missing.cpp`\n- `../outside.go`\n"))
		expectIntakeState(report, code, stderr, atomintakecontract.StateInvalidScope, false)
		gomega.Expect(report.InvalidReasons).To(gomega.And(
			gomega.HaveKeyWithValue("missing.cpp", atomintakecontract.InvalidScopeAmbiguousBareShorthand),
			gomega.HaveKeyWithValue("../outside.go", atomintakecontract.InvalidScopeOutsideProjectRoot),
		))
		gomega.Expect(reportReasonContains(report, "outside the project root")).To(gomega.BeTrue())
		gomega.Expect(reportReasonContains(report, "ambiguous bare shorthand")).To(gomega.BeTrue())
	})

	ginkgo.It("uses the known root-basename allowlist for missing dotfiles", func() {
		root := ginkgo.GinkgoT().TempDir()
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `.gitignore` | Known repository-root basename remains allowed. |\n| `.unrecognized-toolrc` | Unknown bare dotfile shorthand should not authorize a root write. |\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateInvalidScope, false)
		gomega.Expect(report.Invalid).To(gomega.Equal([]string{".unrecognized-toolrc"}))
		gomega.Expect(report.InvalidReasons).To(gomega.HaveKeyWithValue(".unrecognized-toolrc", atomintakecontract.InvalidScopeAmbiguousBareShorthand))
		gomega.Expect(report.Missing).To(gomega.Equal([]string{".gitignore"}))
	})

	ginkgo.It("blocks a symlink escape from the project root", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(outside, "secret.go"), []byte("package outside\n"), 0o644)).To(gomega.Succeed())
		if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		report, code, stderr := runAtomIntakeJSONForTest(root, writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `link/secret.go`\n"))
		expectIntakeState(report, code, stderr, atomintakecontract.StateInvalidScope, false)
		gomega.Expect(report.Invalid).To(gomega.Equal([]string{"link/secret.go"}))
		gomega.Expect(report.InvalidReasons).To(gomega.HaveKeyWithValue("link/secret.go", atomintakecontract.InvalidScopeOutsideProjectRoot))
	})

	ginkgo.It("detects an accepted-result contradiction in a planned atom", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n\n## Result\n\n- Accepted after verification.\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateStaleNeedsRebase, false)
	})

	ginkgo.It("blocks a failed refine best draft", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n")
		specPath := writeAtomSpec(root, "brief.md", "> WARNING: refine exited non-zero — reason=max-iters-exhausted after iter 3.\n\n<!-- idiom:refine-drafted title=atom iter=3 reason=max-iters-exhausted -->\n# Brief\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Modify source. |\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateFailedRefine, false)
		gomega.Expect(SpecDispatchBlocker(mustReadString(specPath))).To(gomega.ContainSubstring("max-iters-exhausted"))
	})

	ginkgo.It("allows a ship-as-is refine draft", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAtomFile(root, "foo.go", "package main\n")
		specPath := writeAtomSpec(root, "brief.md", "<!-- idiom:refine-drafted title=atom iter=1 reason=ship-as-is -->\n# Brief\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Modify source. |\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateDispatchable, true)
	})

	ginkgo.It("blocks dirty scoped paths", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "foo.go", "package main\n", "initial")
		writeAtomFile(root, "foo.go", "package main\n\nfunc dirty() {}\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")
		report, code, stderr := runAtomIntakeJSONForTest(root, specPath)
		expectIntakeState(report, code, stderr, atomintakecontract.StateBlockedDirty, false)
		gomega.Expect(report.Dirty).To(gomega.Equal([]string{"foo.go"}))
	})

	ginkgo.It("makes dirty scope dispatchable only with explicit allowance", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "foo.go", "package main\n", "initial")
		writeAtomFile(root, "foo.go", "package main\n\nfunc dirty() {}\n")
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")
		report, err := RunIntake(IntakeOptions{SpecPath: specPath, ProjectRoot: root, AllowDirty: true}, Dependencies{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(report.State).To(gomega.Equal(atomintakecontract.StateDispatchable))
		gomega.Expect(report.Dispatchable).To(gomega.BeTrue())
	})
})

func writeAtomSpec(root, name, body string) string {
	path := filepath.Join(root, name)
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
	return path
}

func writeAtomFile(root, rel, body string) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
}

func mustReadString(path string) string {
	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return string(body)
}

func reportReasonContains(report IntakeReport, needle string) bool {
	for _, reason := range report.Reasons {
		if strings.Contains(reason, needle) {
			return true
		}
	}
	return false
}

func runAtomIntakeJSONForTest(root, specPath string) (IntakeReport, int, string) {
	report, err := RunIntake(IntakeOptions{SpecPath: specPath, ProjectRoot: root}, Dependencies{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(report.Type).To(gomega.Equal("idiom.atom.intake"))
	gomega.Expect(report.SpecPath).NotTo(gomega.BeEmpty())
	if report.Dispatchable {
		return report, 0, ""
	}
	return report, 1, ""
}

func expectIntakeState(report IntakeReport, code int, stderr string, state atomintakecontract.IntakeState, dispatchable bool) {
	ginkgo.GinkgoHelper()
	if dispatchable {
		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s report=%+v", stderr, report)
	} else {
		gomega.Expect(code).NotTo(gomega.Equal(0), "stderr=%s report=%+v", stderr, report)
	}
	gomega.Expect(report.State).To(gomega.Equal(state))
	gomega.Expect(report.Dispatchable).To(gomega.Equal(dispatchable))
}

func initGitRepoForStaleness() string {
	if _, err := exec.LookPath("git"); err != nil {
		ginkgo.Skip("git is unavailable on PATH")
	}
	root := ginkgo.GinkgoT().TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "test"}} {
		runAtomGit(root, args...)
	}
	return root
}

func gitCommit(root, path, content, message string) {
	writeAtomFile(root, path, content)
	runAtomGit(root, "add", path)
	runAtomGit(root, "commit", "-q", "-m", message)
}

func runAtomGit(root string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git %v\n%s", args, out)
}
