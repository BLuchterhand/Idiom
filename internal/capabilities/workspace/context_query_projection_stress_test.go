package workspace

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"idiom/internal/repository/comprehension"
)

var _ = Describe("compact query projection", func() {
	It("bounds large operational inventories without losing semantic evidence", func() {
		paths := make([]string, 1_205)
		for i := range paths {
			paths[i] = fmt.Sprintf("pkg/generated/file-%04d.go", len(paths)-i-1)
		}

		result := largeInventoryQueryResult(paths)
		projection := newContextQueryProjection(result)
		output, err := renderContextQueryProjection(projection)
		Expect(err).NotTo(HaveOccurred())

		fullOutput, err := json.MarshalIndent(result, "", "  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(output)).To(BeNumerically("<", 20_000), "compact query output should have a stable structural bound, got %d bytes", len(output))
		Expect(len(fullOutput)).To(BeNumerically(">", len(output)*10), "fixture should prove compaction rather than merely small input: full=%d compact=%d", len(fullOutput), len(output))

		Expect(projection.SchemaVersion).To(Equal(contextQueryProjectionSchemaVersion))
		Expect(projection.Claims).To(Equal(result.Claims))
		Expect(projection.Nodes).To(HaveLen(1))
		Expect(projection.Nodes[0].ID).To(Equal("summary://repository/example"))
		Expect(projection.Nodes[0].Properties).To(HaveKeyWithValue("quality_tier", "deterministic"))
		Expect(projection.Edges).To(HaveLen(1))
		Expect(projection.Edges[0].ID).To(Equal("edge://summary/repository"))
		Expect(projection.Edges[0].Evidence).To(HaveKeyWithValue("path", "docs/architecture.md"))
		Expect(projection.UnresolvedGaps).To(Equal([]string{"runtime traces unavailable"}))
		Expect(projection.Confidence.Level).To(Equal("high"))
		Expect(projection.Provenance.Operation).To(Equal("context_query"))

		Expect(projection.AutoRefresh).NotTo(BeNil())
		Expect(projection.AutoRefresh.RefreshedFileCount).To(Equal(len(paths)))
		Expect(projection.AutoRefresh.RefreshedFiles).To(Equal(pathsFromZero(compactQueryInventorySampleLimit)))
		Expect(projection.AutoRefresh.RefreshedFilesOmitted).To(Equal(len(paths) - compactQueryInventorySampleLimit))
		Expect(projection.AutoRefresh.RefreshScopeCount).To(Equal(len(paths)))
		Expect(projection.AutoRefresh.RefreshScopes).To(HaveLen(compactQueryInventorySampleLimit))
		Expect(projection.AutoRefresh.FollowUpRefreshCount).To(Equal(12))
		Expect(projection.AutoRefresh.FollowUpRefreshes).To(HaveLen(compactQueryFollowUpRefreshLimit))
		Expect(projection.AutoRefresh.FollowUpRefreshesOmitted).To(Equal(12 - compactQueryFollowUpRefreshLimit))

		Expect(projection.Freshness).NotTo(BeNil())
		Expect(projection.Freshness.CheckedPathCount).To(Equal(len(paths)))
		Expect(projection.Freshness.CheckedPaths).To(HaveLen(compactQueryInventorySampleLimit))
		Expect(projection.Freshness.CheckedPathsOmitted).To(Equal(len(paths) - compactQueryInventorySampleLimit))
		Expect(projection.Freshness.StalePathCount).To(Equal(len(paths)))
		Expect(projection.Freshness.StalePathsOmitted).To(Equal(len(paths) - compactQueryInventorySampleLimit))
		Expect(projection.Freshness.MissingPathCount).To(Equal(len(paths)))
		Expect(projection.Freshness.MissingPathsOmitted).To(Equal(len(paths) - compactQueryInventorySampleLimit))
		Expect(projection.Freshness.ScopeWarningOutOfCount).To(Equal(len(paths)))
		Expect(projection.Freshness.StalenessWarningFileCount).To(Equal(len(paths)))

		Expect(output).To(ContainSubstring(`"refreshed_file_count":1205`))
		Expect(output).To(ContainSubstring(`"refreshed_files_omitted":1197`))
		Expect(output).To(ContainSubstring(`"checked_path_count":1205`))
		Expect(output).NotTo(ContainSubstring("pkg/generated/file-1204.go"))

		ascending := slices.Clone(paths)
		slices.Reverse(ascending)
		secondOutput, err := renderContextQueryProjection(newContextQueryProjection(largeInventoryQueryResult(ascending)))
		Expect(err).NotTo(HaveOccurred())
		Expect(secondOutput).To(Equal(output), "inventory ordering must not change the model-facing projection")
	})

	It("preserves complete small inventories", func() {
		paths := []string{"pkg/b.go", "pkg/a.go"}
		projection := newContextQueryProjection(largeInventoryQueryResult(paths))

		Expect(projection.AutoRefresh.RefreshedFileCount).To(Equal(2))
		Expect(projection.AutoRefresh.RefreshedFiles).To(Equal([]string{"pkg/a.go", "pkg/b.go"}))
		Expect(projection.AutoRefresh.RefreshedFilesOmitted).To(BeZero())
		Expect(projection.Freshness.CheckedPaths).To(Equal([]string{"pkg/a.go", "pkg/b.go"}))
		Expect(projection.Freshness.CheckedPathsOmitted).To(BeZero())
	})

	It("keeps exact inventory totals when deterministic samples deduplicate paths", func() {
		paths := []string{"pkg/b.go", "pkg/a.go", "pkg/a.go"}
		projection := newContextQueryProjection(largeInventoryQueryResult(paths))

		Expect(projection.AutoRefresh.RefreshedFileCount).To(Equal(len(paths)))
		Expect(projection.AutoRefresh.RefreshedFiles).To(Equal([]string{"pkg/a.go", "pkg/b.go"}))
		Expect(projection.AutoRefresh.RefreshedFilesOmitted).To(Equal(1))
		Expect(projection.Freshness.CheckedPathCount).To(Equal(len(paths)))
		Expect(projection.Freshness.CheckedPaths).To(Equal([]string{"pkg/a.go", "pkg/b.go"}))
		Expect(projection.Freshness.CheckedPathsOmitted).To(Equal(1))
	})

	It("normalizes cyclic follow-up projections without recursively cloning them", func() {
		cycle := make([]contextQueryRefresh, 1)
		cycle[0] = contextQueryRefresh{Scope: "child"}
		cycle[0].FollowUpRefreshes = cycle
		projection := contextQueryProjection{
			AutoRefresh: &contextQueryRefresh{FollowUpRefreshes: cycle},
		}

		output, err := renderContextQueryProjection(projection)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Valid([]byte(output))).To(BeTrue())
		Expect(cycle[0].FollowUpRefreshes).To(HaveLen(1), "normalization must not mutate caller-owned recursive slices")

		var rendered contextQueryProjection
		Expect(json.Unmarshal([]byte(output), &rendered)).To(Succeed())
		Expect(rendered.AutoRefresh.FollowUpRefreshCount).To(Equal(1))
		Expect(rendered.AutoRefresh.FollowUpRefreshes).To(HaveLen(1))
		Expect(rendered.AutoRefresh.FollowUpRefreshes[0].FollowUpRefreshCount).To(Equal(1))
		Expect(rendered.AutoRefresh.FollowUpRefreshes[0].FollowUpRefreshes).To(BeEmpty())
		Expect(rendered.AutoRefresh.FollowUpRefreshes[0].FollowUpRefreshesOmitted).To(Equal(1))
	})

	It("canonicalizes unsupported nested map keys without address-dependent samples", func() {
		first, second := 1, 2
		firstSummary := compactQueryMapSummary(reflect.ValueOf(map[any]string{
			&first:   "first",
			&second:  "second",
			"stable": "string",
			int64(7): "integer",
		}))
		third, fourth := 3, 4
		secondSummary := compactQueryMapSummary(reflect.ValueOf(map[any]string{
			&third:   "third",
			&fourth:  "fourth",
			"stable": "string",
			int64(7): "integer",
		}))

		Expect(firstSummary).To(Equal(secondSummary))
		Expect(firstSummary.ItemCount).To(Equal(4))
		Expect(firstSummary.Samples).To(Equal([]string{"7", "key_type:*int", "key_type:*int", "stable"}))
		for _, sample := range firstSummary.Samples {
			Expect(sample).NotTo(ContainSubstring("0x"))
		}
	})

	It("summarizes pointer-interface cycles without addresses and renders deterministically", func() {
		var cyclic any
		cyclic = &cyclic
		structured := &struct {
			Value any
		}{Value: cyclic}
		metadata := map[string]any{
			"a_cycle":  cyclic,
			"b_list":   []any{cyclic},
			"c_struct": structured,
		}
		result := comprehension.QueryResult{
			Nodes: []comprehension.NodeSummary{{
				ID:         "symbol://cycle#Value",
				Kind:       "symbol",
				Confidence: "derived",
				Properties: metadata,
			}},
			Edges: []comprehension.EdgeSummary{{
				ID:         "edge://cycle",
				Kind:       "references",
				SourceID:   "symbol://cycle#Value",
				TargetID:   "symbol://cycle#Value",
				Confidence: "derived",
				Evidence:   metadata,
			}},
		}

		projection := newContextQueryProjection(result)
		cycleSummary, ok := projection.Nodes[0].Properties["a_cycle"].(contextQueryMetadataSummary)
		Expect(ok).To(BeTrue())
		Expect(cycleSummary.Preview).To(Equal("cyclic_reference"))
		Expect(cycleSummary.Type).To(ContainSubstring("interface"))
		listSummary, ok := projection.Nodes[0].Properties["b_list"].(contextQueryMetadataSummary)
		Expect(ok).To(BeTrue())
		Expect(listSummary.Samples).To(HaveLen(1))
		Expect(listSummary.Samples[0]).To(ContainSubstring("cyclic_reference"))
		structSummary, ok := projection.Nodes[0].Properties["c_struct"].(contextQueryMetadataSummary)
		Expect(ok).To(BeTrue())
		Expect(structSummary.Type).To(ContainSubstring("struct"))
		Expect(structSummary.Preview).To(BeEmpty())

		firstOutput, err := renderContextQueryProjection(projection)
		Expect(err).NotTo(HaveOccurred())
		secondOutput, err := renderContextQueryProjection(projection)
		Expect(err).NotTo(HaveOccurred())
		thirdOutput, err := renderContextQueryProjection(newContextQueryProjection(result))
		Expect(err).NotTo(HaveOccurred())
		Expect(firstOutput).To(Equal(secondOutput))
		Expect(firstOutput).To(Equal(thirdOutput))
		Expect(json.Valid([]byte(firstOutput))).To(BeTrue())
		Expect(firstOutput).NotTo(ContainSubstring("0x"))
		Expect(len(firstOutput)).To(BeNumerically("<=", maxContextQueryProjectionBytes))
	})

	It("hard-bounds oversized semantic results and nested metadata", func() {
		result := oversizedSemanticQueryResult()
		projection := newContextQueryProjection(result)

		Expect(result.Claims).To(HaveLen(3_003), "projection must not mutate or narrow the internal result")
		Expect(result.Nodes).To(HaveLen(2_001))
		Expect(result.Edges).To(HaveLen(4_002))
		Expect(result.UnresolvedGaps).To(HaveLen(1_001))
		Expect(projection.ClaimCount).To(Equal(3_003))
		Expect(projection.Claims).To(HaveLen(contextQueryClaimLimit))
		Expect(projection.ClaimsOmitted).To(Equal(3_003 - contextQueryClaimLimit))
		Expect(projection.NodeCount).To(Equal(2_001))
		Expect(projection.Nodes).To(HaveLen(contextQueryNodeLimit))
		Expect(projection.NodesOmitted).To(Equal(2_001 - contextQueryNodeLimit))
		Expect(projection.EdgeCount).To(Equal(4_002))
		Expect(projection.Edges).To(HaveLen(contextQueryEdgeLimit))
		Expect(projection.EdgesOmitted).To(Equal(4_002 - contextQueryEdgeLimit))
		Expect(projection.UnresolvedGapCount).To(Equal(1_001))
		Expect(projection.UnresolvedGaps).To(HaveLen(contextQueryGapLimit))
		Expect(projection.UnresolvedGapsOmitted).To(Equal(1_001 - contextQueryGapLimit))

		node := projection.Nodes[0]
		Expect(node.ID).To(HavePrefix("symbol://internal/runtime/agent/runner_core.go#Runner.Run"))
		Expect(node.Path).To(HavePrefix("internal/runtime/agent/runner_core.go"))
		Expect(node.StartLine).To(Equal(41))
		Expect(node.EndLine).To(Equal(73))
		Expect(node.PropertyCount).To(Equal(7))
		Expect(node.Properties).To(HaveLen(contextQueryMetadataEntryLimit))
		Expect(node.PropertiesOmitted).To(Equal(1))
		Expect(node.Properties).To(HaveKeyWithValue("a_path", "internal/runtime/agent/runner_core.go"))
		Expect(node.Properties).To(HaveKeyWithValue("b_line", 41))

		stringSummary, ok := node.Properties["c_huge_string"].(contextQueryMetadataSummary)
		Expect(ok).To(BeTrue())
		Expect(stringSummary.Type).To(Equal("string"))
		Expect(stringSummary.ByteCount).To(BeNumerically(">", len(stringSummary.Preview)))
		Expect(stringSummary.OmittedBytes).To(BeNumerically(">", 0))
		nestedSummary, ok := node.Properties["d_nested"].(contextQueryMetadataSummary)
		Expect(ok).To(BeTrue())
		Expect(nestedSummary.Type).To(Equal("object"))
		Expect(nestedSummary.ItemCount).To(Equal(10_003))
		Expect(nestedSummary.Samples).To(HaveLen(contextQueryMetadataSampleLimit))
		Expect(nestedSummary.Samples).To(Equal([]string{"nested-key-000", "nested-key-001", "nested-key-002", "nested-key-003"}))
		Expect(nestedSummary.ItemsOmitted).To(Equal(10_003 - contextQueryMetadataSampleLimit))
		listSummary, ok := node.Properties["e_list"].(contextQueryMetadataSummary)
		Expect(ok).To(BeTrue())
		Expect(listSummary.Type).To(Equal("array"))
		Expect(listSummary.ItemCount).To(Equal(513))
		Expect(listSummary.Samples).To(HaveLen(contextQueryMetadataSampleLimit))
		Expect(projection.Edges[0].EvidenceCount).To(Equal(7))
		Expect(projection.Edges[0].Evidence).To(HaveLen(contextQueryMetadataEntryLimit))

		output, err := renderContextQueryProjection(projection)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Valid([]byte(output))).To(BeTrue())
		Expect(len(output)).To(BeNumerically("<=", maxContextQueryProjectionBytes))
		Expect(output).NotTo(ContainSubstring("UNIQUE-OVERSIZED-TAIL-MARKER"))

		var rendered contextQueryProjection
		Expect(json.Unmarshal([]byte(output), &rendered)).To(Succeed())
		Expect(rendered.SchemaVersion).To(Equal(contextQueryProjectionSchemaVersion))
		Expect(rendered.ClaimCount).To(Equal(3_003))
		Expect(rendered.ClaimsOmitted).To(Equal(3_003 - len(rendered.Claims)))
		Expect(rendered.Claims).NotTo(BeEmpty(), "hard-byte reduction should preserve at least one semantic claim")
		Expect(rendered.NodeCount).To(Equal(2_001))
		Expect(rendered.NodesOmitted).To(Equal(2_001 - len(rendered.Nodes)))
		Expect(rendered.Nodes).NotTo(BeEmpty(), "hard-byte reduction should preserve at least one core node handle")
		Expect(rendered.Nodes[0].ID).To(HavePrefix("symbol://internal/runtime/agent/runner_core.go#Runner.Run"))
		Expect(rendered.EdgeCount).To(Equal(4_002))
		Expect(rendered.EdgesOmitted).To(Equal(4_002 - len(rendered.Edges)))
		Expect(rendered.Edges).NotTo(BeEmpty(), "hard-byte reduction should preserve at least one relationship")
		Expect(rendered.UnresolvedGapCount).To(Equal(1_001))
		Expect(rendered.UnresolvedGapsOmitted).To(Equal(1_001 - len(rendered.UnresolvedGaps)))

		secondOutput, err := renderContextQueryProjection(projection)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondOutput).To(Equal(output), "rendering must be deterministic and must not mutate its input")

		callerProjection := oversizedCallerProjection(result)
		callerOutput, err := renderContextQueryProjection(callerProjection)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Valid([]byte(callerOutput))).To(BeTrue())
		Expect(len(callerOutput)).To(BeNumerically("<=", maxContextQueryProjectionBytes))
		var callerRendered contextQueryProjection
		Expect(json.Unmarshal([]byte(callerOutput), &callerRendered)).To(Succeed())
		Expect(callerRendered.ClaimCount).To(Equal(len(result.Claims)))
		Expect(callerRendered.NodeCount).To(Equal(len(result.Nodes)))
		Expect(callerRendered.EdgeCount).To(Equal(len(result.Edges)))
		Expect(callerRendered.UnresolvedGapCount).To(Equal(len(result.UnresolvedGaps)))
		Expect(callerRendered.Nodes).To(HaveLen(contextQueryNodeLimit))
		Expect(callerRendered.Edges).To(HaveLen(contextQueryEdgeLimit))
	})
})

func oversizedSemanticQueryResult() comprehension.QueryResult {
	hugeValue := strings.Repeat("\x00oversized-value-", 512) + "UNIQUE-OVERSIZED-TAIL-MARKER"
	nested := make(map[string]any, 10_003)
	for i := range 10_003 {
		nested[fmt.Sprintf("nested-key-%03d", i)] = hugeValue
	}
	list := make([]any, 513)
	for i := range list {
		list[i] = hugeValue
	}
	metadata := map[string]any{
		"a_path":        "internal/runtime/agent/runner_core.go",
		"b_line":        41,
		"c_huge_string": hugeValue,
		"d_nested":      nested,
		"e_list":        list,
		"f_bool":        true,
		"g_extra":       "counted but omitted",
	}
	hugeNodeID := "symbol://internal/runtime/agent/runner_core.go#Runner.Run/" + hugeValue
	hugeEdgeID := "edge://Runner.Run/calls/execute/" + hugeValue
	hugePath := "internal/runtime/agent/runner_core.go/" + hugeValue
	hugeClaim := "Runner.Run coordinates execution: " + hugeValue
	hugeGap := "runtime evidence unavailable: " + hugeValue

	result := comprehension.QueryResult{
		Claims:         make([]string, 3_003),
		Nodes:          make([]comprehension.NodeSummary, 2_001),
		Edges:          make([]comprehension.EdgeSummary, 4_002),
		UnresolvedGaps: make([]string, 1_001),
	}
	for i := range result.Claims {
		result.Claims[i] = hugeClaim
	}
	for i := range result.Nodes {
		result.Nodes[i] = comprehension.NodeSummary{
			ID:            hugeNodeID,
			Kind:          "symbol",
			Path:          hugePath,
			Language:      "go",
			QualifiedName: "Runner.Run",
			StartLine:     41,
			EndLine:       73,
			Confidence:    "syntax",
			Properties:    metadata,
		}
	}
	for i := range result.Edges {
		result.Edges[i] = comprehension.EdgeSummary{
			ID:         hugeEdgeID,
			Kind:       "calls",
			SourceID:   hugeNodeID,
			TargetID:   "symbol://internal/runtime/agent/execution.go#execute/" + hugeValue,
			Confidence: "syntax",
			Evidence:   metadata,
		}
	}
	for i := range result.UnresolvedGaps {
		result.UnresolvedGaps[i] = hugeGap
	}

	return result
}

func oversizedCallerProjection(result comprehension.QueryResult) contextQueryProjection {
	projection := contextQueryProjection{
		Claims:         slices.Clone(result.Claims),
		Nodes:          make([]contextQueryNode, len(result.Nodes)),
		Edges:          make([]contextQueryEdge, len(result.Edges)),
		UnresolvedGaps: slices.Clone(result.UnresolvedGaps),
	}
	for i, node := range result.Nodes {
		projection.Nodes[i] = contextQueryNode{
			ID:            node.ID,
			Kind:          node.Kind,
			Path:          node.Path,
			Language:      node.Language,
			QualifiedName: node.QualifiedName,
			StartLine:     node.StartLine,
			EndLine:       node.EndLine,
			Confidence:    node.Confidence,
			Properties:    node.Properties,
		}
	}
	for i, edge := range result.Edges {
		projection.Edges[i] = contextQueryEdge{
			ID:         edge.ID,
			Kind:       edge.Kind,
			SourceID:   edge.SourceID,
			TargetID:   edge.TargetID,
			Confidence: edge.Confidence,
			Evidence:   edge.Evidence,
		}
	}

	return projection
}

func largeInventoryQueryResult(paths []string) comprehension.QueryResult {
	refreshScopes := make([]string, len(paths))
	for i, path := range paths {
		refreshScopes[i] = strings.TrimSuffix(path, ".go")
	}

	followUps := make([]comprehension.RefreshReport, 12)
	for i := range followUps {
		followUps[i] = comprehension.RefreshReport{
			Attempted:      true,
			Scope:          fmt.Sprintf("pkg/follow-up-%02d", i),
			FilesIndexed:   1,
			FreshAfter:     true,
			RefreshedFiles: []string{fmt.Sprintf("pkg/follow-up-%02d.go", i)},
		}
	}

	refresh := &comprehension.RefreshReport{
		Attempted:         true,
		Scope:             ".",
		Language:          "go",
		Reason:            comprehension.FreshnessReasonStaleUnboundedGraph,
		IndexID:           "index-123",
		FilesIndexed:      len(paths),
		SourceFileCount:   len(paths),
		SymbolsIndexed:    2_410,
		EdgesIndexed:      4_820,
		FreshAfter:        true,
		RefreshedFiles:    slices.Clone(paths),
		RefreshScopes:     refreshScopes,
		FollowUpRefreshes: followUps,
	}

	return comprehension.QueryResult{
		IndexID:     "index-123",
		StoreKind:   "sqlite-graph-v1",
		Fresh:       true,
		Resolution:  "summary",
		GeneratedAt: "2026-07-12T10:07:10Z",
		Confidence: comprehension.GraphResultConfidence{
			Level:  "high",
			Source: "freshness_decision",
		},
		Provenance: comprehension.GraphResultProvenance{
			Source:      "context_graph",
			Operation:   "context_query",
			StoreKind:   "sqlite-graph-v1",
			IndexID:     "index-123",
			GeneratedAt: "2026-07-12T10:07:10Z",
		},
		AutoRefresh: refresh,
		Freshness: &comprehension.FreshnessDecision{
			Policy:                comprehension.FreshnessReadThrough,
			Operation:             "context_query",
			IndexID:               "index-123",
			StoreKind:             "sqlite-graph-v1",
			GeneratedAt:           "2026-07-12T10:07:10Z",
			FreshForScope:         true,
			GlobalFresh:           true,
			Scope:                 ".",
			Reason:                comprehension.FreshnessReasonStaleUnboundedGraph,
			Refresh:               refresh,
			CheckedPaths:          slices.Clone(paths),
			CheckedPathCount:      len(paths),
			StalePaths:            slices.Clone(paths),
			StalePathCount:        len(paths),
			MissingPaths:          slices.Clone(paths),
			MissingPathCount:      len(paths),
			ScopeWarning:          "query returned paths outside the checked scope",
			ScopeWarningOutOf:     slices.Clone(paths),
			StalenessWarning:      "some query paths were stale before refresh",
			StalenessWarningFiles: slices.Clone(paths),
		},
		StaleEvidence: comprehension.StaleEvidenceSafety{
			State:         "fresh",
			Policy:        comprehension.FreshnessReadThrough,
			FreshForScope: true,
			GlobalFresh:   true,
		},
		Claims: []string{"summary \"repository summary\" maps to . (derived confidence)"},
		Nodes: []comprehension.NodeSummary{{
			ID:            "summary://repository/example",
			Kind:          "summary",
			Path:          ".",
			QualifiedName: "repository summary",
			Confidence:    "derived",
			Properties: map[string]any{
				"quality_tier": "deterministic",
				"files":        len(paths),
			},
		}},
		Edges: []comprehension.EdgeSummary{{
			ID:         "edge://summary/repository",
			Kind:       "summarizes",
			SourceID:   "summary://repository/example",
			TargetID:   "repo://example",
			Confidence: "derived",
			Evidence: map[string]any{
				"path": "docs/architecture.md",
				"line": 3,
			},
		}},
		UnresolvedGaps: []string{"runtime traces unavailable"},
	}
}

func pathsFromZero(count int) []string {
	paths := make([]string, count)
	for i := range paths {
		paths[i] = fmt.Sprintf("pkg/generated/file-%04d.go", i)
	}

	return paths
}
