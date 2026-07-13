package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("context query projection development workflow", ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	ginkgo.It("keeps complete discovery facts while bounding a refreshed repository graph for the model", func() {
		const (
			fileCount          = 12
			symbolPairsPerFile = 4
			symbolCount        = fileCount * symbolPairsPerFile * 2
			edgeCount          = symbolCount + fileCount*symbolPairsPerFile
		)

		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/contextprojection\n\ngo 1.22\n")

		expectedPaths := make([]string, 0, fileCount)
		expectedSymbols := make([]string, 0, symbolCount)
		for fileIndex := range fileCount {
			relativePath := fmt.Sprintf("internal/scenario/component_%02d.go", fileIndex)
			expectedPaths = append(expectedPaths, relativePath)
			writeWorkspaceFile(filepath.Join(root, filepath.FromSlash(relativePath)), contextProjectionScenarioSource(fileIndex, symbolPairsPerFile, false))

			for pairIndex := range symbolPairsPerFile {
				expectedSymbols = append(expectedSymbols, fmt.Sprintf("%s#ScenarioAction%02d_%02d", relativePath, fileIndex, pairIndex))
			}
			for pairIndex := range symbolPairsPerFile {
				expectedSymbols = append(expectedSymbols, fmt.Sprintf("%s#ScenarioHelper%02d_%02d", relativePath, fileIndex, pairIndex))
			}
		}

		manager := NewManager(config.Workspace{Root: root, CWD: root})
		indexed := manager.Execute(context.Background(), ToolRequest{
			Tool:     ToolNameContextIndex,
			Language: "go",
		})
		gomega.Expect(indexed.Error).To(gomega.BeEmpty(), indexed.Output)

		for fileIndex, relativePath := range expectedPaths {
			writeWorkspaceFile(filepath.Join(root, filepath.FromSlash(relativePath)), contextProjectionScenarioSource(fileIndex, symbolPairsPerFile, true))
		}

		request := ToolRequest{
			Tool:            ToolNameContextQuery,
			Pattern:         "Scenario",
			Resolution:      "symbol",
			Limit:           symbolCount,
			IncludeEvidence: true,
			FreshnessPolicy: "read_through",
		}
		result := manager.Execute(context.Background(), request)
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Output)
		gomega.Expect(json.Valid([]byte(result.Output))).To(gomega.BeTrue())
		gomega.Expect(len(result.Output)).To(gomega.BeNumerically("<=", maxContextQueryProjectionBytes))

		var projection contextQueryProjection
		gomega.Expect(json.Unmarshal([]byte(result.Output), &projection)).To(gomega.Succeed())
		gomega.Expect(projection.SchemaVersion).To(gomega.Equal(contextQueryProjectionSchemaVersion))
		gomega.Expect(projection.ClaimCount).To(gomega.Equal(symbolCount))
		gomega.Expect(projection.Claims).To(gomega.HaveLen(contextQueryClaimLimit))
		gomega.Expect(projection.ClaimsOmitted).To(gomega.Equal(symbolCount - contextQueryClaimLimit))
		gomega.Expect(projection.NodeCount).To(gomega.Equal(symbolCount))
		gomega.Expect(projection.Nodes).To(gomega.HaveLen(contextQueryNodeLimit))
		gomega.Expect(projection.NodesOmitted).To(gomega.Equal(symbolCount - contextQueryNodeLimit))
		gomega.Expect(projection.EdgeCount).To(gomega.Equal(edgeCount))
		gomega.Expect(projection.Edges).To(gomega.HaveLen(contextQueryEdgeLimit))
		gomega.Expect(projection.EdgesOmitted).To(gomega.Equal(edgeCount - contextQueryEdgeLimit))
		gomega.Expect(projection.UnresolvedGapCount).To(gomega.BeZero())

		expectedRetainedHandles := make([]string, contextQueryNodeLimit)
		for i, symbol := range expectedSymbols[:contextQueryNodeLimit] {
			expectedRetainedHandles[i] = "symbol://" + symbol
		}
		retainedHandles := make([]string, len(projection.Nodes))
		for i, node := range projection.Nodes {
			retainedHandles[i] = node.ID
			gomega.Expect(node.PropertyCount).To(gomega.Equal(3))
			gomega.Expect(node.Properties).To(gomega.HaveLen(3))
		}
		gomega.Expect(retainedHandles).To(gomega.Equal(expectedRetainedHandles))
		for _, edge := range projection.Edges {
			gomega.Expect(edge.EvidenceCount).To(gomega.Equal(2))
			gomega.Expect(edge.Evidence).To(gomega.HaveLen(2))
		}

		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.CandidatePaths).To(gomega.Equal(expectedPaths))
		gomega.Expect(result.Facts.CandidateSymbols).To(gomega.Equal(expectedSymbols))
		gomega.Expect(result.Facts.CandidatePaths).To(gomega.HaveLen(fileCount))
		gomega.Expect(result.Facts.CandidateSymbols).To(gomega.HaveLen(symbolCount))
		gomega.Expect(result.Facts.CandidatePaths).To(gomega.ContainElement(expectedPaths[len(expectedPaths)-1]))
		gomega.Expect(result.Facts.CandidateSymbols).To(gomega.ContainElement(expectedSymbols[len(expectedSymbols)-1]))
		gomega.Expect(slices.Contains(retainedHandles, "symbol://"+expectedSymbols[len(expectedSymbols)-1])).To(gomega.BeFalse())

		gomega.Expect(projection.AutoRefresh).NotTo(gomega.BeNil())
		gomega.Expect(projection.AutoRefresh.RefreshedFileCount).To(gomega.Equal(fileCount))
		gomega.Expect(projection.AutoRefresh.RefreshedFiles).To(gomega.Equal(expectedPaths[:compactQueryInventorySampleLimit]))
		gomega.Expect(projection.AutoRefresh.RefreshedFilesOmitted).To(gomega.Equal(fileCount - compactQueryInventorySampleLimit))
		gomega.Expect(projection.Freshness).NotTo(gomega.BeNil())
		gomega.Expect(projection.Freshness.Refresh).NotTo(gomega.BeNil())
		gomega.Expect(projection.Freshness.Refresh.RefreshedFileCount).To(gomega.Equal(fileCount))
		gomega.Expect(projection.Freshness.Refresh.RefreshedFiles).To(gomega.Equal(expectedPaths[:compactQueryInventorySampleLimit]))
		gomega.Expect(projection.Freshness.Refresh.RefreshedFilesOmitted).To(gomega.Equal(fileCount - compactQueryInventorySampleLimit))

		telemetry := ToolTelemetryFields(ToolNameContextQuery, result.Output)
		gomega.Expect(telemetry["context_nodes"]).To(gomega.Equal(contextQueryNodeLimit))
		gomega.Expect(telemetry["context_edges"]).To(gomega.Equal(contextQueryEdgeLimit))
		gomega.Expect(telemetry["context_auto_refresh_refreshed_files"]).To(gomega.Equal(fileCount))
		gomega.Expect(telemetry["context_auto_refresh_refreshed_file_samples"]).To(gomega.Equal(compactQueryInventorySampleLimit))
		gomega.Expect(telemetry["context_auto_refresh_refreshed_files_omitted"]).To(gomega.Equal(fileCount - compactQueryInventorySampleLimit))
		gomega.Expect(telemetry["context_freshness_refresh_refreshed_files"]).To(gomega.Equal(fileCount))
		gomega.Expect(telemetry["context_freshness_refresh_refreshed_file_samples"]).To(gomega.Equal(compactQueryInventorySampleLimit))
		gomega.Expect(telemetry["context_freshness_refresh_refreshed_files_omitted"]).To(gomega.Equal(fileCount - compactQueryInventorySampleLimit))

		stable := manager.Execute(context.Background(), request)
		gomega.Expect(stable.Error).To(gomega.BeEmpty(), stable.Output)
		gomega.Expect(stable.Facts).NotTo(gomega.BeNil())
		gomega.Expect(stable.Facts.CandidatePaths).To(gomega.Equal(expectedPaths))
		gomega.Expect(stable.Facts.CandidateSymbols).To(gomega.Equal(expectedSymbols))

		var stableProjection contextQueryProjection
		gomega.Expect(json.Unmarshal([]byte(stable.Output), &stableProjection)).To(gomega.Succeed())
		stableHandles := make([]string, len(stableProjection.Nodes))
		for i, node := range stableProjection.Nodes {
			stableHandles[i] = node.ID
		}
		gomega.Expect(stableHandles).To(gomega.Equal(retainedHandles))
	})
})

func contextProjectionScenarioSource(fileIndex int, pairCount int, refreshed bool) string {
	refreshMarker := "initial index"
	if refreshed {
		refreshMarker = "refreshed index"
	}

	source := fmt.Sprintf("package scenario\n\n// Fixture state: %s.\n", refreshMarker)
	for pairIndex := range pairCount {
		source += fmt.Sprintf(`
func ScenarioAction%02d_%02d(value int) int {
	return ScenarioHelper%02d_%02d(value)
}

func ScenarioHelper%02d_%02d(value int) int {
	return value + %d
}
`, fileIndex, pairIndex, fileIndex, pairIndex, fileIndex, pairIndex, fileIndex+pairIndex)
	}

	return source
}
