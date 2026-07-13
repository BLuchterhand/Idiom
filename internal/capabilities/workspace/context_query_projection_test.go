package workspace

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/repository/comprehension"
)

var _ = ginkgo.Describe("context query projection", func() {
	ginkgo.It("keeps full typed discovery facts while model output samples refresh paths", func() {
		refreshPaths := make([]string, 1_001)
		for i := range refreshPaths {
			refreshPaths[i] = fmt.Sprintf("generated/refresh-%04d.go", i)
		}

		semanticNodes := []comprehension.NodeSummary{
			{ID: "symbol://internal/runtime/agent/runner_core.go#Runner.Run", Path: "internal/runtime/agent/runner_core.go", QualifiedName: "Runner.Run", Confidence: "syntax"},
			{ID: "file://README.md", Path: "README.md", Confidence: "exact"},
		}
		for i := range 40 {
			path := fmt.Sprintf("internal/generated/query-result-%02d.go", i)
			name := fmt.Sprintf("QueryResult%02d", i)
			semanticNodes = append(semanticNodes, comprehension.NodeSummary{
				ID:            "symbol://" + path + "#" + name,
				Path:          path,
				QualifiedName: name,
				Confidence:    "syntax",
			})
		}

		result := comprehension.QueryResult{
			Nodes: semanticNodes,
			AutoRefresh: &comprehension.RefreshReport{
				Attempted:      true,
				FilesIndexed:   len(refreshPaths),
				RefreshedFiles: refreshPaths,
			},
		}
		result.Freshness = &comprehension.FreshnessDecision{
			Policy:           comprehension.FreshnessReadThrough,
			Operation:        ToolNameContextQuery,
			FreshForScope:    true,
			GlobalFresh:      true,
			Refresh:          result.AutoRefresh,
			CheckedPaths:     refreshPaths,
			CheckedPathCount: len(refreshPaths),
		}

		projection := newContextQueryProjection(result)
		output, err := renderContextQueryProjection(projection)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Nodes).To(gomega.HaveLen(42), "model-facing projection must not narrow the internal query result")
		gomega.Expect(projection.NodeCount).To(gomega.Equal(42))
		gomega.Expect(projection.Nodes).To(gomega.HaveLen(contextQueryNodeLimit))
		gomega.Expect(projection.NodesOmitted).To(gomega.Equal(42 - contextQueryNodeLimit))
		gomega.Expect(len(output)).To(gomega.BeNumerically("<=", maxContextQueryProjectionBytes))
		gomega.Expect(projection.AutoRefresh.RefreshedFileCount).To(gomega.Equal(len(refreshPaths)))
		gomega.Expect(projection.AutoRefresh.RefreshedFiles).To(gomega.HaveLen(8))
		gomega.Expect(output).NotTo(gomega.ContainSubstring("generated/refresh-1000.go"))

		telemetry := ContextTelemetryFields(ToolNameContextQuery, output)
		gomega.Expect(telemetry["context_auto_refresh_refreshed_files"]).To(gomega.Equal(len(refreshPaths)))
		gomega.Expect(telemetry["context_auto_refresh_refreshed_file_samples"]).To(gomega.Equal(8))
		gomega.Expect(telemetry["context_auto_refresh_refreshed_files_omitted"]).To(gomega.Equal(len(refreshPaths) - 8))
		gomega.Expect(telemetry["context_freshness_refresh_refreshed_files"]).To(gomega.Equal(len(refreshPaths)))
		gomega.Expect(telemetry["context_freshness_refresh_refreshed_file_samples"]).To(gomega.Equal(8))
		gomega.Expect(telemetry["context_freshness_refresh_refreshed_files_omitted"]).To(gomega.Equal(len(refreshPaths) - 8))

		facts := contextQueryToolResultFacts(result)
		gomega.Expect(facts).NotTo(gomega.BeNil())
		gomega.Expect(facts.DiscoveryStrength).To(gomega.Equal(toolfacts.DiscoveryStrengthCandidateOnly))
		gomega.Expect(facts.CandidatePaths).To(gomega.HaveLen(len(result.Nodes)))
		gomega.Expect(slices.Contains(facts.CandidatePaths, "internal/generated/query-result-39.go")).To(gomega.BeTrue())
		gomega.Expect(slices.Contains(facts.CandidateSymbols, "internal/runtime/agent/runner_core.go#Runner.Run")).To(gomega.BeTrue())
		gomega.Expect(slices.Contains(facts.CandidateSymbols, "internal/generated/query-result-39.go#QueryResult39")).To(gomega.BeTrue())
	})

	ginkgo.It("keeps legacy refresh telemetry list length semantics", func() {
		fields := ContextTelemetryFields(ToolNameContextQuery, `{
			"auto_refresh": {"refreshed_files": ["a.go", "b.go", "c.go"]},
			"freshness": {"refresh": {"refreshed_files": ["a.go", "b.go"]}}
		}`)

		gomega.Expect(fields["context_auto_refresh_refreshed_files"]).To(gomega.Equal(3))
		gomega.Expect(fields).NotTo(gomega.HaveKey("context_auto_refresh_refreshed_file_samples"))
		gomega.Expect(fields["context_freshness_refresh_refreshed_files"]).To(gomega.Equal(2))
		gomega.Expect(fields).NotTo(gomega.HaveKey("context_freshness_refresh_refreshed_file_samples"))
	})
})
