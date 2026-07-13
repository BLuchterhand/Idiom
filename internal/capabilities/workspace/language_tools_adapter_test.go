package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/repository/semantic/languagetools"
)

var _ = ginkgo.Describe("language tools adapter", func() {
	ginkgo.It("language tool result preserves facts", func() {
		paths := []string{"internal/app.go"}
		result := fromLanguageToolResult(languagetools.ToolResult{
			Tool:  "go_diagnostics",
			Facts: &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: paths},
		})
		paths[0] = "mutated.go"
		gomega.Expect(result.Facts).NotTo(gomega.BeNil(), fmt.Sprintf("fromLanguageToolResult facts = %#v", result.Facts))
		gomega.Expect(slices.Equal(result.Facts.DiagnosticEvidencePaths, []string{"internal/app.go"})).To(gomega.BeTrue(), fmt.Sprintf("fromLanguageToolResult facts = %#v", result.Facts))

	})
	ginkgo.It("keeps language tool dispatch available across concurrent writes and publishes post-write source", ginkgo.Label("race-contract"), func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/language-lifecycle\n\ngo 1.22\n")
		writeTestFile(filepath.Join(root, "symbols.go"), "package lifecycle\n\nfunc InitialSymbol() {}\n")

		mgr := NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		})
		initial := mgr.Execute(context.Background(), ToolRequest{Tool: "go_find_symbol", Pattern: "InitialSymbol"})
		gomega.Expect(initial.Error).To(gomega.BeEmpty(), fmt.Sprintf("initial language query failed: %#v", initial))
		gomega.Expect(initial.Output).To(gomega.ContainSubstring("InitialSymbol"), fmt.Sprintf("initial language query missed the fixture: %#v", initial))

		type operationResult struct {
			kind   string
			result ToolResult
		}

		const operationCount = 16
		results := make(chan operationResult, operationCount*2)
		var operations sync.WaitGroup
		for i := 0; i < operationCount; i++ {
			i := i
			operations.Add(2)
			go func() {
				defer operations.Done()
				results <- operationResult{
					kind: "write",
					result: mgr.Execute(context.Background(), ToolRequest{
						Tool:    ToolNameAppendFile,
						Path:    "events.log",
						Content: fmt.Sprintf("event-%02d\n", i),
					}),
				}
			}()
			go func() {
				defer operations.Done()
				results <- operationResult{
					kind:   "query",
					result: mgr.Execute(context.Background(), ToolRequest{Tool: "go_find_symbol", Pattern: "InitialSymbol"}),
				}
			}()
		}
		operations.Wait()
		close(results)

		for completed := range results {
			gomega.Expect(completed.result.Error).To(gomega.BeEmpty(), fmt.Sprintf("concurrent %s failed: %#v", completed.kind, completed.result))
			if completed.kind == "query" {
				gomega.Expect(completed.result.Output).To(gomega.ContainSubstring("InitialSymbol"), fmt.Sprintf("concurrent language query missed stable source: %#v", completed.result))
			}
		}

		write := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameAppendFile,
			Path:    "symbols.go",
			Content: "\nfunc AddedAfterWrite() {}\n",
		})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), fmt.Sprintf("source append failed: %#v", write))

		fresh := mgr.Execute(context.Background(), ToolRequest{Tool: "go_find_symbol", Pattern: "AddedAfterWrite"})
		gomega.Expect(fresh.Error).To(gomega.BeEmpty(), fmt.Sprintf("post-write language query failed: %#v", fresh))
		gomega.Expect(fresh.Output).To(gomega.ContainSubstring("AddedAfterWrite"), fmt.Sprintf("post-write language query reused stale source: %#v", fresh))
	})
})
