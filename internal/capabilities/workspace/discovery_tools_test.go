package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/evidence/eval"
	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
)

var _ = ginkgo.Describe("discovery tools", func() {
	ginkgo.It("creates the discovery ignore entry through the Git metadata root", func() {
		root := ginkgo.GinkgoT().TempDir()
		output, err := exec.Command("git", "-C", root, "init").CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("git init failed: %s", strings.TrimSpace(string(output))))

		excludePath, err := gitInfoExcludePath(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.RemoveAll(filepath.Dir(excludePath))).To(gomega.Succeed())
		gomega.Expect(ensureDiscoveryArtifactsIgnored(root)).To(gomega.Succeed())
		data, err := os.ReadFile(excludePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(data)).To(gomega.ContainSubstring(".idiom/"))

		gomega.Expect(ensureDiscoveryArtifactsIgnored(root)).To(gomega.Succeed())
		data, err = os.ReadFile(excludePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(strings.Count(string(data), ".idiom/")).To(gomega.Equal(1))
	})

	ginkgo.It("generic discovery tools are advertised", func() {
		tools := ToolManifestTools(ToolManifestToolOptions{})
		for _, want := range []string{
			"harness_audit",
			"harness_feedback",
			"file_stats",
			"code_navigate",
			"read_only_capability",
			"source_sanity",
			"context_index",
			"context_status",
			"context_query",
			"context_zoom",
			"context_pack",
			"context_impact",
			"context_trace_pack",
			"discovery_query",
			"structural_search",
			"dependency_graph",
			"history_range",
			"history_symbol",
			"blame_range",
			"ctags_symbols",
			"semgrep_scan",
			"joern_scan",
		} {
			gomega.Expect(slices.Contains(tools, want)).To(gomega.BeTrue(), fmt.Sprintf("expected manifest tools to include %q, got %#v", want, tools))

		}

		defs := BuildToolDefinitions(tools)

		var names []string

		for _, def := range defs {
			names = append(names, def.Name)
		}

		for _, want := range []string{"harness_audit", "harness_feedback", "file_stats", "code_navigate", "read_only_capability", "source_sanity", "context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_impact", "context_trace_pack", "discovery_query", "structural_search", "dependency_graph", "history_range", "blame_range"} {
			gomega.Expect(slices.Contains(names, want)).To(gomega.BeTrue(), fmt.Sprintf("expected tool definitions to include %q, got %#v", want, names))

		}
	})
	ginkgo.It("context impact schema allows path only requests", func() {
		defs := BuildToolDefinitions([]string{"context_impact"})
		gomega.Expect(defs).To(gomega.HaveLen(1), fmt.Sprintf("expected one context_impact definition, got %#v", defs))

		var schema struct {
			Required []string `json:"required"`
		}
		gomega.Expect(json.Unmarshal([]byte(defs[0].ParametersJSON), &schema)).To(gomega.Succeed())
		gomega.Expect(slices.Contains(schema.Required, "task")).To(gomega.BeFalse(), fmt.Sprintf("context_impact schema should allow path-only requests, required=%#v", schema.Required))

	})
	ginkgo.It("history range schema allows path only requests", func() {
		defs := BuildToolDefinitions([]string{"history_range"})
		gomega.Expect(defs).To(gomega.HaveLen(1), fmt.Sprintf("expected one history_range definition, got %#v", defs))

		var schema struct {
			Required []string `json:"required"`
		}
		gomega.Expect(json.Unmarshal([]byte(defs[0].ParametersJSON), &schema)).To(gomega.Succeed())
		gomega.Expect(slices.Contains(schema.Required, "path")).To(gomega.BeTrue(), fmt.Sprintf("history_range schema must require path, required=%#v", schema.Required))

		for _, field := range []string{"start_line", "end_line"} {
			gomega.Expect(slices.Contains(schema.Required, field)).To(gomega.BeFalse(), fmt.Sprintf("history_range schema should allow path-only requests, required=%#v", schema.Required))

		}
	})
	ginkgo.It("context tools build query and report stale symbols", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/contexttools\n\ngo 1.22\n")

		sourcePath := filepath.Join(root, "pkg", "pkg.go")
		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		var buildReport struct {
			FilesIndexed     int      `json:"files_indexed"`
			SymbolsIndexed   int      `json:"symbols_indexed"`
			Languages        []string `json:"languages"`
			CollectorReports []struct {
				Name      string   `json:"name"`
				Languages []string `json:"languages"`
			} `json:"collector_reports"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &buildReport)).To(gomega.Succeed())
		gomega.Expect(buildReport.FilesIndexed).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected index report: %#v", buildReport))
		gomega.Expect(buildReport.SymbolsIndexed).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("unexpected index report: %#v", buildReport))
		gomega.Expect(buildReport.Languages).To(gomega.ContainElement("go"), fmt.Sprintf("unexpected index report: %#v", buildReport))
		gomega.Expect(slices.ContainsFunc(buildReport.CollectorReports, func(report struct {
			Name      string   `json:"name"`
			Languages []string `json:"languages"`
		}) bool {

			return report.Name == "gopls" && slices.Contains(report.Languages, "go")
		})).To(gomega.BeTrue(), fmt.Sprintf("expected context_index to report Go backend enrichment, got %#v", buildReport.CollectorReports))

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_query", Pattern: "Run", Resolution: "symbol", IncludeEvidence: true})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_query returned error: %v\n%s", result.Error, result.Output))

		var queryReport struct {
			Nodes []struct {
				ID         string `json:"id"`
				Confidence string `json:"confidence"`
			} `json:"nodes"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &queryReport)).To(gomega.Succeed())
		gomega.Expect(slices.ContainsFunc(queryReport.Nodes, func(node struct {
			ID         string `json:"id"`
			Confidence string `json:"confidence"`
		}) bool {

			return node.ID == "symbol://pkg/pkg.go#Run" && node.Confidence != ""
		})).To(gomega.BeTrue(), fmt.Sprintf("expected Run symbol handle with confidence, got %#v", queryReport.Nodes))

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "Plan a change to Run", RequiredTests: []string{"go test ./..."}})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_pack returned error: %v\n%s", result.Error, result.Output))

		var packReport struct {
			ArtifactPath   string `json:"artifact_path"`
			CandidateFiles []struct {
				Path string `json:"path"`
				Role string `json:"role"`
			} `json:"candidate_files"`
			Tests []struct {
				Command string `json:"command"`
			} `json:"tests"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &packReport)).To(gomega.Succeed())
		gomega.Expect(packReport.ArtifactPath).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected context_pack artifact and editable candidate, got %#v", packReport))
		gomega.Expect(slices.ContainsFunc(packReport.CandidateFiles, func(file struct {
			Path string `json:"path"`
			Role string `json:"role"`
		}) bool {

			return file.Path == "pkg/pkg.go" && file.Role == "editable"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected context_pack artifact and editable candidate, got %#v", packReport))
		gomega.Expect(slices.ContainsFunc(packReport.Tests, func(test struct {
			Command string `json:"command"`
		}) bool {

			return test.Command == "go test ./..."
		})).To(gomega.BeTrue(), fmt.Sprintf("expected context_pack to preserve required tests, got %#v", packReport.Tests))

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_impact", Task: "Plan a change to Run", RequiredTests: []string{"go test ./..."}})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_impact returned error: %v\n%s", result.Error, result.Output))

		var impactReport struct {
			ContextViewPath string `json:"context_view_path"`
			ImpactScope     []struct {
				Kind    string  `json:"kind"`
				Path    string  `json:"path"`
				Rank    float64 `json:"rank"`
				Signals []struct {
					Kind string `json:"kind"`
				} `json:"signals"`
			} `json:"impact_scope"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &impactReport)).To(gomega.Succeed())
		gomega.Expect(impactReport.ContextViewPath).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected context_impact ranking with file signal, got %#v", impactReport))
		gomega.Expect(slices.ContainsFunc(impactReport.ImpactScope, func(item struct {
			Kind    string  `json:"kind"`
			Path    string  `json:"path"`
			Rank    float64 `json:"rank"`
			Signals []struct {
				Kind string `json:"kind"`
			} `json:"signals"`
		}) bool {

			return item.Kind == "file" && item.Path == "pkg/pkg.go" && item.Rank > 0 && len(item.Signals) > 0
		})).To(gomega.BeTrue(), fmt.Sprintf("expected context_impact ranking with file signal, got %#v", impactReport))

		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			Fresh        bool     `json:"fresh"`
			StaleFiles   []string `json:"stale_files"`
			StaleSymbols int      `json:"stale_symbols"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Fresh).To(gomega.BeFalse(), fmt.Sprintf("expected stale file and symbols, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFiles).To(gomega.ContainElement("pkg/pkg.go"), fmt.Sprintf("expected stale file and symbols, got %#v", statusReport))
		gomega.Expect(statusReport.StaleSymbols).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected stale file and symbols, got %#v", statusReport))

	})
	ginkgo.It("context impact supports path only request", func() {
		root := ginkgo.GinkgoT().TempDir()
		targetPath := filepath.Join("internal", "comprehension", "pack.go")
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/pathimpact\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, targetPath), "package comprehension\n\nfunc Pack() string { return \"DO_NOT_LEAK_BODY\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_impact", Path: targetPath})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_impact returned error: %v\n%s", result.Error, result.Output))

		assertNoPrescriptiveGraphOutput("context_impact path-only", result.Summary+"\n"+result.Output)
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("DO_NOT_LEAK_BODY"), fmt.Sprintf("context_impact path-only output leaked raw source body:\n%s", result.Output))

		var report struct {
			Task            string `json:"task"`
			ContextViewPath string `json:"context_view_path"`
			Confidence      struct {
				Level string `json:"level"`
			} `json:"confidence"`
			Provenance struct {
				Operation string `json:"operation"`
				IndexID   string `json:"index_id"`
			} `json:"provenance"`
			IndexID     string `json:"index_id"`
			ImpactScope []struct {
				Kind       string `json:"kind"`
				Path       string `json:"path"`
				Confidence string `json:"confidence"`
				Signals    []struct {
					Kind string `json:"kind"`
				} `json:"signals"`
			} `json:"impact_scope"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.Task).To(gomega.Equal("Graph-backed impact evidence for "+targetPath), fmt.Sprintf("expected derived path evidence task, got %q", report.Task))
		gomega.Expect(report.ContextViewPath).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected structured graph metadata, got %#v", report))
		gomega.Expect(report.Confidence.Level).To(gomega.Equal("high"), fmt.Sprintf("expected structured graph metadata, got %#v", report))
		gomega.Expect(report.Provenance.Operation).To(gomega.Equal("context_impact"), fmt.Sprintf("expected structured graph metadata, got %#v", report))
		gomega.Expect(report.Provenance.IndexID).To(gomega.Equal(report.IndexID), fmt.Sprintf("expected structured graph metadata, got %#v", report))
		gomega.Expect(slices.ContainsFunc(report.ImpactScope, func(item struct {
			Kind       string `json:"kind"`
			Path       string `json:"path"`
			Confidence string `json:"confidence"`
			Signals    []struct {
				Kind string `json:"kind"`
			} `json:"signals"`
		}) bool {

			return item.Kind == "file" && item.Path == targetPath && item.Confidence != "" && len(item.Signals) > 0
		})).To(gomega.BeTrue(), fmt.Sprintf("expected path-only impact scope for %s, got %#v", targetPath, report.ImpactScope))

	})
	ginkgo.It("context status includes structured freshness confidence and provenance", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statusmetadata\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var status struct {
			IndexID          string `json:"index_id"`
			StoreKind        string `json:"store_kind"`
			GeneratedAt      string `json:"generated_at"`
			Fresh            bool   `json:"fresh"`
			StaleFileCount   int    `json:"stale_file_count"`
			MissingFileCount int    `json:"missing_file_count"`
			StalePathCount   int    `json:"stale_path_count"`
			Confidence       struct {
				Level  string `json:"level"`
				Source string `json:"source"`
			} `json:"confidence"`
			Provenance struct {
				Source      string `json:"source"`
				Operation   string `json:"operation"`
				StoreKind   string `json:"store_kind"`
				IndexID     string `json:"index_id"`
				GeneratedAt string `json:"generated_at"`
			} `json:"provenance"`
			Freshness struct {
				Policy           string `json:"policy"`
				Operation        string `json:"operation"`
				IndexID          string `json:"index_id"`
				StoreKind        string `json:"store_kind"`
				GeneratedAt      string `json:"generated_at"`
				FreshForScope    bool   `json:"fresh_for_scope"`
				GlobalFresh      bool   `json:"global_fresh"`
				CheckedPathCount int    `json:"checked_path_count"`
				StalePathCount   int    `json:"stale_path_count"`
				MissingPathCount int    `json:"missing_path_count"`
			} `json:"freshness"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &status)).To(gomega.Succeed())
		gomega.Expect(status.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected fresh indexed status metadata, got %#v", status))
		gomega.Expect(status.IndexID).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected fresh indexed status metadata, got %#v", status))
		gomega.Expect(status.StoreKind).To(gomega.Equal("sqlite-graph-v1"), fmt.Sprintf("expected fresh indexed status metadata, got %#v", status))
		gomega.Expect(status.GeneratedAt).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected fresh indexed status metadata, got %#v", status))
		gomega.Expect(status.StaleFileCount).To(gomega.BeNumerically("==", 0), fmt.Sprintf("fresh status should report zero stale/missing counts, got %#v", status))
		gomega.Expect(status.MissingFileCount).To(gomega.BeNumerically("==", 0), fmt.Sprintf("fresh status should report zero stale/missing counts, got %#v", status))
		gomega.Expect(status.StalePathCount).To(gomega.BeNumerically("==", 0), fmt.Sprintf("fresh status should report zero stale/missing counts, got %#v", status))
		gomega.Expect(status.Confidence.Level).To(gomega.Equal("high"), fmt.Sprintf("expected high confidence backed by freshness decision, got %#v", status.Confidence))
		gomega.Expect(status.Confidence.Source).To(gomega.Equal("freshness_decision"), fmt.Sprintf("expected high confidence backed by freshness decision, got %#v", status.Confidence))
		gomega.Expect(status.Provenance.Source).To(gomega.Equal("context_graph"), fmt.Sprintf("unexpected status provenance: %#v", status.Provenance))
		gomega.Expect(status.Provenance.Operation).To(gomega.Equal("context_status"), fmt.Sprintf("unexpected status provenance: %#v", status.Provenance))
		gomega.Expect(status.Provenance.IndexID).To(gomega.Equal(status.IndexID), fmt.Sprintf("unexpected status provenance: %#v", status.Provenance))
		gomega.Expect(status.Provenance.GeneratedAt).To(gomega.Equal(status.GeneratedAt), fmt.Sprintf("unexpected status provenance: %#v", status.Provenance))
		gomega.Expect(status.Provenance.StoreKind).To(gomega.Equal(status.StoreKind), fmt.Sprintf("unexpected status provenance: %#v", status.Provenance))
		gomega.Expect(status.Freshness.Policy).To(gomega.Equal("report_only"), fmt.Sprintf("unexpected status freshness contract: %#v", status.Freshness))
		gomega.Expect(status.Freshness.Operation).To(gomega.Equal("context_status"), fmt.Sprintf("unexpected status freshness contract: %#v", status.Freshness))
		gomega.Expect(status.Freshness.IndexID).To(gomega.Equal(status.IndexID), fmt.Sprintf("unexpected status freshness contract: %#v", status.Freshness))
		gomega.Expect(status.Freshness.StoreKind).To(gomega.Equal(status.StoreKind), fmt.Sprintf("unexpected status freshness contract: %#v", status.Freshness))
		gomega.Expect(status.Freshness.GeneratedAt).To(gomega.Equal(status.GeneratedAt), fmt.Sprintf("unexpected status freshness contract: %#v", status.Freshness))
		gomega.Expect(status.Freshness.FreshForScope).To(gomega.BeTrue(), fmt.Sprintf("unexpected status freshness contract: %#v", status.Freshness))
		gomega.Expect(status.Freshness.GlobalFresh).To(gomega.BeTrue(), fmt.Sprintf("unexpected status freshness contract: %#v", status.Freshness))

	})
	ginkgo.It("context status reports structured stale path counts", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statusstale\n\ngo 1.22\n")

		sourcePath := filepath.Join(root, "pkg", "pkg.go")
		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var status struct {
			Fresh          bool     `json:"fresh"`
			StaleFiles     []string `json:"stale_files"`
			StaleFileCount int      `json:"stale_file_count"`
			StalePathCount int      `json:"stale_path_count"`
			Confidence     struct {
				Level  string `json:"level"`
				Source string `json:"source"`
				Reason string `json:"reason"`
			} `json:"confidence"`
			Freshness struct {
				FreshForScope  bool     `json:"fresh_for_scope"`
				GlobalFresh    bool     `json:"global_fresh"`
				Reason         string   `json:"reason"`
				StalePaths     []string `json:"stale_paths"`
				StalePathCount int      `json:"stale_path_count"`
			} `json:"freshness"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &status)).To(gomega.Succeed())
		gomega.Expect(status.Fresh).To(gomega.BeFalse(), fmt.Sprintf("expected one stale path in status, got %#v", status))
		gomega.Expect(status.StaleFileCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected one stale path in status, got %#v", status))
		gomega.Expect(status.StalePathCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected one stale path in status, got %#v", status))
		gomega.Expect(status.StaleFiles).To(gomega.ContainElement("pkg/pkg.go"), fmt.Sprintf("expected one stale path in status, got %#v", status))
		gomega.Expect(status.Freshness.FreshForScope).To(gomega.BeFalse(), fmt.Sprintf("expected structured stale freshness fields, got %#v", status.Freshness))
		gomega.Expect(status.Freshness.GlobalFresh).To(gomega.BeFalse(), fmt.Sprintf("expected structured stale freshness fields, got %#v", status.Freshness))
		gomega.Expect(status.Freshness.Reason).To(gomega.Equal("stale_unbounded_graph"), fmt.Sprintf("expected structured stale freshness fields, got %#v", status.Freshness))
		gomega.Expect(status.Freshness.StalePathCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected structured stale freshness fields, got %#v", status.Freshness))
		gomega.Expect(status.Freshness.StalePaths).To(gomega.ContainElement("pkg/pkg.go"), fmt.Sprintf("expected structured stale freshness fields, got %#v", status.Freshness))
		gomega.Expect(status.Confidence.Level).To(gomega.Equal("low"), fmt.Sprintf("expected low confidence from stale freshness decision, got %#v", status.Confidence))
		gomega.Expect(status.Confidence.Source).To(gomega.Equal("freshness_decision"), fmt.Sprintf("expected low confidence from stale freshness decision, got %#v", status.Confidence))
		gomega.Expect(status.Confidence.Reason).To(gomega.Equal("stale_unbounded_graph"), fmt.Sprintf("expected low confidence from stale freshness decision, got %#v", status.Confidence))

	})
	ginkgo.It("graph backed outputs carry confidence provenance and legacy fields", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/graphoutputs\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return helper() }\n\nfunc helper() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		query := mgr.Execute(context.Background(), ToolRequest{Tool: "context_query", Pattern: "Run", Resolution: "symbol", IncludeEvidence: true})
		gomega.Expect(query.Error).To(gomega.Equal(""), fmt.Sprintf("context_query returned error: %v\n%s", query.Error, query.Output))

		var queryReport struct {
			IndexID     string `json:"index_id"`
			StoreKind   string `json:"store_kind"`
			GeneratedAt string `json:"generated_at"`
			Confidence  struct {
				Level string `json:"level"`
			} `json:"confidence"`
			Provenance struct {
				Source    string `json:"source"`
				Operation string `json:"operation"`
				IndexID   string `json:"index_id"`
			} `json:"provenance"`
			Nodes []struct {
				ID         string `json:"id"`
				Confidence string `json:"confidence"`
			} `json:"nodes"`
		}
		gomega.Expect(json.Unmarshal([]byte(query.Output), &queryReport)).To(gomega.Succeed())
		gomega.Expect(queryReport.IndexID).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected query confidence/provenance metadata, got %#v", queryReport))
		gomega.Expect(queryReport.StoreKind).To(gomega.Equal("sqlite-graph-v1"), fmt.Sprintf("expected query confidence/provenance metadata, got %#v", queryReport))
		gomega.Expect(queryReport.GeneratedAt).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected query confidence/provenance metadata, got %#v", queryReport))
		gomega.Expect(queryReport.Confidence.Level).To(gomega.Equal("high"), fmt.Sprintf("expected query confidence/provenance metadata, got %#v", queryReport))
		gomega.Expect(queryReport.Provenance.Source).To(gomega.Equal("context_graph"), fmt.Sprintf("expected query confidence/provenance metadata, got %#v", queryReport))
		gomega.Expect(queryReport.Provenance.Operation).To(gomega.Equal("context_query"), fmt.Sprintf("expected query confidence/provenance metadata, got %#v", queryReport))
		gomega.Expect(queryReport.Provenance.IndexID).To(gomega.Equal(queryReport.IndexID), fmt.Sprintf("expected query confidence/provenance metadata, got %#v", queryReport))
		gomega.Expect(slices.ContainsFunc(queryReport.Nodes, func(node struct {
			ID         string `json:"id"`
			Confidence string `json:"confidence"`
		}) bool {

			return node.ID == "symbol://pkg/pkg.go#Run" && node.Confidence != ""
		})).To(gomega.BeTrue(), fmt.Sprintf("expected node-level confidence to remain present, got %#v", queryReport.Nodes))

		zoom := mgr.Execute(context.Background(), ToolRequest{Tool: "context_zoom", Handle: "symbol://pkg/pkg.go#Run", Resolution: "symbol"})
		gomega.Expect(zoom.Error).To(gomega.Equal(""), fmt.Sprintf("context_zoom returned error: %v\n%s", zoom.Error, zoom.Output))

		var zoomReport struct {
			IndexID    string `json:"index_id"`
			Confidence struct {
				Level string `json:"level"`
			} `json:"confidence"`
			Provenance struct {
				Operation string `json:"operation"`
				IndexID   string `json:"index_id"`
			} `json:"provenance"`
			Node *struct {
				ID string `json:"id"`
			} `json:"node"`
		}
		gomega.Expect(json.Unmarshal([]byte(zoom.Output), &zoomReport)).To(gomega.Succeed())
		gomega.Expect(zoomReport.Node).NotTo(gomega.BeNil(), fmt.Sprintf("expected zoom metadata and node, got %#v", zoomReport))
		gomega.Expect(zoomReport.Node.ID).To(gomega.Equal("symbol://pkg/pkg.go#Run"), fmt.Sprintf("expected zoom metadata and node, got %#v", zoomReport))
		gomega.Expect(zoomReport.Confidence.Level).To(gomega.Equal("high"), fmt.Sprintf("expected zoom metadata and node, got %#v", zoomReport))
		gomega.Expect(zoomReport.Provenance.Operation).To(gomega.Equal("context_zoom"), fmt.Sprintf("expected zoom metadata and node, got %#v", zoomReport))
		gomega.Expect(zoomReport.Provenance.IndexID).To(gomega.Equal(zoomReport.IndexID), fmt.Sprintf("expected zoom metadata and node, got %#v", zoomReport))

		pack := mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "Plan a change to Run", RequiredTests: []string{"go test ./..."}})
		gomega.Expect(pack.Error).To(gomega.Equal(""), fmt.Sprintf("context_pack returned error: %v\n%s", pack.Error, pack.Output))

		var packReport struct {
			ArtifactPath string `json:"artifact_path"`
			IndexID      string `json:"index_id"`
			Fresh        bool   `json:"fresh"`
			Confidence   struct {
				Level string `json:"level"`
			} `json:"confidence"`
			Provenance struct {
				Source       string `json:"source"`
				Operation    string `json:"operation"`
				IndexID      string `json:"index_id"`
				ArtifactPath string `json:"artifact_path"`
			} `json:"provenance"`
			Cache struct {
				State        string `json:"state"`
				Source       string `json:"source"`
				CacheKey     string `json:"cache_key"`
				Invalidation struct {
					Policy  string `json:"policy"`
					Sources []struct {
						Source string `json:"source"`
						State  string `json:"state"`
					} `json:"sources"`
				} `json:"invalidation"`
			} `json:"cache"`
			Contract struct {
				SchemaVersion string `json:"schema_version"`
				Cache         struct {
					State string `json:"state"`
				} `json:"cache"`
				UnresolvedGaps []struct {
					Reason string `json:"reason"`
					State  string `json:"state"`
				} `json:"unresolved_gaps"`
			} `json:"contract"`
			Freshness struct {
				IndexID       string `json:"index_id"`
				FreshForScope bool   `json:"fresh_for_scope"`
			} `json:"freshness"`
			CandidateFiles []struct {
				Path          string `json:"path"`
				Role          string `json:"role"`
				Confidence    string `json:"confidence"`
				RankerReasons []struct {
					Reason string `json:"reason"`
					State  string `json:"state"`
				} `json:"ranker_reasons"`
			} `json:"candidate_files"`
			Tests []struct {
				Command string `json:"command"`
			} `json:"tests"`
		}
		gomega.Expect(json.Unmarshal([]byte(pack.Output), &packReport)).To(gomega.Succeed())
		gomega.Expect(packReport.ArtifactPath).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Confidence.Level).To(gomega.Equal("high"), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Provenance.Source).To(gomega.Equal("context_graph"), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Provenance.Operation).To(gomega.Equal("context_pack"), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Provenance.IndexID).To(gomega.Equal(packReport.IndexID), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Provenance.ArtifactPath).To(gomega.Equal(packReport.ArtifactPath), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Freshness.IndexID).To(gomega.Equal(packReport.IndexID), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Freshness.FreshForScope).To(gomega.BeTrue(), fmt.Sprintf("expected pack metadata and legacy fields, got %#v", packReport))
		gomega.Expect(packReport.Cache.State).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected graph-view contract/cache metadata, got cache=%#v contract=%#v", packReport.Cache, packReport.Contract))
		gomega.Expect(packReport.Cache.CacheKey).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected graph-view contract/cache metadata, got cache=%#v contract=%#v", packReport.Cache, packReport.Contract))
		gomega.Expect(packReport.Cache.Invalidation.Policy).To(gomega.Equal("content_addressed_read_through"), fmt.Sprintf("expected graph-view contract/cache metadata, got cache=%#v contract=%#v", packReport.Cache, packReport.Contract))
		gomega.Expect(packReport.Contract.SchemaVersion).To(gomega.Equal(comprehension.GraphViewResultContractVersion), fmt.Sprintf("expected graph-view contract/cache metadata, got cache=%#v contract=%#v", packReport.Cache, packReport.Contract))
		gomega.Expect(packReport.Contract.Cache.State).To(gomega.Equal(packReport.Cache.State), fmt.Sprintf("expected graph-view contract/cache metadata, got cache=%#v contract=%#v", packReport.Cache, packReport.Contract))
		gomega.Expect(packReportSourcePresent(packReport.Cache.Invalidation.Sources, "ranker_weights")).To(gomega.BeTrue(), fmt.Sprintf("expected ranker/runtime invalidation sources, got %#v", packReport.Cache.Invalidation.Sources))
		gomega.Expect(packReportSourcePresent(packReport.Cache.Invalidation.Sources, "runtime_traces")).To(gomega.BeTrue(), fmt.Sprintf("expected ranker/runtime invalidation sources, got %#v", packReport.Cache.Invalidation.Sources))
		gomega.Expect(slices.ContainsFunc(packReport.CandidateFiles, func(file struct {
			Path          string `json:"path"`
			Role          string `json:"role"`
			Confidence    string `json:"confidence"`
			RankerReasons []struct {
				Reason string `json:"reason"`
				State  string `json:"state"`
			} `json:"ranker_reasons"`
		}) bool {

			return file.Path == "pkg/pkg.go" && file.Role == "editable" && file.Confidence != "" && packReportRankerReasonPresent(file.RankerReasons, "role_floor")
		})).To(gomega.BeTrue(), fmt.Sprintf("expected legacy pack candidates/tests with confidence, got files=%#v tests=%#v", packReport.CandidateFiles, packReport.Tests))
		gomega.Expect(slices.ContainsFunc(packReport.Tests, func(test struct {
			Command string `json:"command"`
		}) bool {

			return test.Command == "go test ./..."
		})).To(gomega.BeTrue(), fmt.Sprintf("expected legacy pack candidates/tests with confidence, got files=%#v tests=%#v", packReport.CandidateFiles, packReport.Tests))

		impact := mgr.Execute(context.Background(), ToolRequest{Tool: "context_impact", Task: "Plan a change to Run"})
		gomega.Expect(impact.Error).To(gomega.Equal(""), fmt.Sprintf("context_impact returned error: %v\n%s", impact.Error, impact.Output))

		var impactReport struct {
			ContextViewPath string `json:"context_view_path"`
			Confidence      struct {
				Level string `json:"level"`
			} `json:"confidence"`
			Provenance struct {
				Operation string `json:"operation"`
				IndexID   string `json:"index_id"`
			} `json:"provenance"`
			IndexID     string `json:"index_id"`
			ImpactScope []struct {
				Path       string `json:"path"`
				Confidence string `json:"confidence"`
			} `json:"impact_scope"`
		}
		gomega.Expect(json.Unmarshal([]byte(impact.Output), &impactReport)).To(gomega.Succeed())
		gomega.Expect(impactReport.ContextViewPath).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected impact metadata and context view, got %#v", impactReport))
		gomega.Expect(impactReport.Confidence.Level).To(gomega.Equal("high"), fmt.Sprintf("expected impact metadata and context view, got %#v", impactReport))
		gomega.Expect(impactReport.Provenance.Operation).To(gomega.Equal("context_impact"), fmt.Sprintf("expected impact metadata and context view, got %#v", impactReport))
		gomega.Expect(impactReport.Provenance.IndexID).To(gomega.Equal(impactReport.IndexID), fmt.Sprintf("expected impact metadata and context view, got %#v", impactReport))
		gomega.Expect(slices.ContainsFunc(impactReport.ImpactScope, func(item struct {
			Path       string `json:"path"`
			Confidence string `json:"confidence"`
		}) bool {

			return item.Path == "pkg/pkg.go" && item.Confidence != ""
		})).To(gomega.BeTrue(), fmt.Sprintf("expected impact scope confidence, got %#v", impactReport.ImpactScope))

	})
	ginkgo.It("graph backed context outputs do not prescribe next tools", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/neutraloutputs\n\ngo 1.22\n")

		sourcePath := filepath.Join(root, "pkg", "pkg.go")
		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		outputs := map[string]string{}

		for name, req := range map[string]ToolRequest{
			"context_status": {Tool: "context_status"},
			"context_query":  {Tool: "context_query", Query: "zzzz-no-match", Resolution: "symbol"},
			"context_zoom":   {Tool: "context_zoom", Handle: "symbol://pkg/pkg.go#Missing", Resolution: "symbol"},
			"context_pack":   {Tool: "context_pack", Task: "zzzz-no-match"},
			"context_impact": {Tool: "context_impact", Task: "zzzz-no-match"},
		} {
			result := mgr.Execute(context.Background(), req)
			gomega.Expect(result.Error).To(gomega.Or(
				gomega.BeEmpty(),
				gomega.Equal("freshness_blocker"),
			), fmt.Sprintf("%s returned error: %v\n%s", name, result.Error, result.Output))

			outputs[name] = result.Summary + "\n" + result.Error + "\n" + result.Output
		}

		discovery := mgr.Execute(context.Background(), ToolRequest{
			Tool:     "discovery_query",
			Purpose:  "impact_analysis",
			Path:     ".",
			Pattern:  "Run",
			Language: "go",
			MaxDepth: 2,
		})
		gomega.Expect(discovery.Error).To(gomega.Equal(""), fmt.Sprintf("discovery_query returned error: %v\n%s", discovery.Error, discovery.Output))

		packet := readDiscoveryQueryPacket(root, discovery.Output)
		gomega.Expect(packetHasLayer(packet, "context_pack")).To(gomega.BeTrue(), fmt.Sprintf("expected graph-backed discovery_query context_pack layer, got %#v", packetLayerNames(packet)))

		outputs["discovery_query"] = discovery.Summary + "\n" + discovery.Output + "\n" + mustMarshalTestJSON(packet)

		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

		staleStatus := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status"})
		gomega.Expect(staleStatus.Error).To(gomega.Equal(""), fmt.Sprintf("stale context_status returned error: %v\n%s", staleStatus.Error, staleStatus.Output))

		outputs["context_status_stale"] = staleStatus.Summary + "\n" + staleStatus.Output

		for name, output := range outputs {
			assertNoPrescriptiveGraphOutput(name, output)
		}
	})
	ginkgo.It("context pack exposes structured unresolved gaps", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/structuredgaps\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "zzzzzzzzqqq", Mode: "plan"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_pack returned error: %v\n%s", result.Error, result.Output))

		var report struct {
			UnresolvedGaps       []string `json:"unresolved_gaps"`
			UnresolvedGapDetails []struct {
				Reason             string `json:"reason"`
				State              string `json:"state"`
				CandidateFileCount int    `json:"candidate_file_count"`
			} `json:"unresolved_gap_details"`
			Contract struct {
				UnresolvedGaps []struct {
					Reason string `json:"reason"`
					State  string `json:"state"`
				} `json:"unresolved_gaps"`
			} `json:"contract"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(slices.ContainsFunc(report.UnresolvedGapDetails, func(gap struct {
			Reason             string `json:"reason"`
			State              string `json:"state"`
			CandidateFileCount int    `json:"candidate_file_count"`
		}) bool {

			return gap.Reason == "no_graph_matches" && gap.State == "empty" && gap.CandidateFileCount == 0
		})).To(gomega.BeTrue(), fmt.Sprintf("expected structured no-match gap, got legacy=%#v details=%#v", report.UnresolvedGaps, report.UnresolvedGapDetails))
		gomega.Expect(slices.ContainsFunc(report.Contract.UnresolvedGaps, func(gap struct {
			Reason string `json:"reason"`
			State  string `json:"state"`
		}) bool {

			return gap.Reason == "no_graph_matches" && gap.State == "empty"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected contract unresolved gap mirror, got %#v", report.Contract.UnresolvedGaps))

		assertNoPrescriptiveGraphOutput("context_pack_structured_gaps", result.Output)
	})
	ginkgo.It("context pack auto refreshes bounded stale scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/autorefresh\n\ngo 1.22\n")

		sourcePath := filepath.Join(root, "pkg", "pkg.go")
		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "Plan a change to Run", Path: "pkg", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_pack returned error: %v\n%s", result.Error, result.Output))

		var packReport struct {
			Fresh       bool `json:"fresh"`
			AutoRefresh struct {
				Attempted    bool   `json:"attempted"`
				Scope        string `json:"scope"`
				Reason       string `json:"reason"`
				FilesIndexed int    `json:"files_indexed"`
				FreshAfter   bool   `json:"fresh_after"`
			} `json:"auto_refresh"`
			CandidateFiles []struct {
				Path  string `json:"path"`
				Stale bool   `json:"stale"`
			} `json:"candidate_files"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &packReport)).To(gomega.Succeed())
		gomega.Expect(packReport.AutoRefresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected bounded stale-scope auto-refresh, got %#v", packReport.AutoRefresh))
		gomega.Expect(packReport.AutoRefresh.Scope).To(gomega.Equal("pkg"), fmt.Sprintf("expected bounded stale-scope auto-refresh, got %#v", packReport.AutoRefresh))
		gomega.Expect(packReport.AutoRefresh.Reason).To(gomega.Equal("stale_scope_files"), fmt.Sprintf("expected bounded stale-scope auto-refresh, got %#v", packReport.AutoRefresh))
		gomega.Expect(packReport.AutoRefresh.FilesIndexed).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected bounded stale-scope auto-refresh, got %#v", packReport.AutoRefresh))
		gomega.Expect(packReport.AutoRefresh.FreshAfter).To(gomega.BeTrue(), fmt.Sprintf("expected bounded stale-scope auto-refresh, got %#v", packReport.AutoRefresh))
		gomega.Expect(packReport.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected pack to use refreshed graph, got %#v", packReport))
		gomega.Expect(slices.ContainsFunc(packReport.CandidateFiles, func(file struct {
			Path  string `json:"path"`
			Stale bool   `json:"stale"`
		}) bool {

			return file.Path == "pkg/pkg.go" && !file.Stale
		})).To(gomega.BeTrue(), fmt.Sprintf("expected refreshed non-stale candidate, got %#v", packReport.CandidateFiles))

	})
	ginkgo.It("context pack required edit paths bound freshness", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/requiredfreshness\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "target.go"), "package pkg\n\nfunc Target() string { return \"ok\" }\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "noisy_a.go"), "package pkg\n\nfunc NoisyA() string { return \"old\" }\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "noisy_b.go"), "package pkg\n\nfunc NoisyB() string { return \"old\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		writeWorkspaceFile(filepath.Join(root, "pkg", "noisy_a.go"), "package pkg\n\nfunc NoisyA() string { return \"new\" }\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "noisy_b.go"), "package pkg\n\nfunc NoisyB() string { return \"new\" }\n")

		result = mgr.Execute(context.Background(), ToolRequest{
			Tool:              "context_pack",
			Task:              "Plan a noisy change",
			RequiredEditPaths: []string{"pkg/target.go"},
			Limit:             1,
			FreshnessMaxFiles: 1,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_pack should use required_edit_paths as freshness scope, got error=%q output=%s", result.Error, result.Output))

		var packReport struct {
			Freshness      comprehension.FreshnessDecision `json:"freshness"`
			CandidateFiles []struct {
				Path          string                       `json:"path"`
				RankerReasons []comprehension.RankerReason `json:"ranker_reasons"`
			} `json:"candidate_files"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &packReport)).To(gomega.Succeed())
		gomega.Expect(packReport.Freshness.FreshForScope).To(gomega.BeTrue(), fmt.Sprintf("expected required edit path to bound freshness, got %#v", packReport.Freshness))
		gomega.Expect(packReport.Freshness.BudgetExceeded).To(gomega.BeFalse(), fmt.Sprintf("expected required edit path to bound freshness, got %#v", packReport.Freshness))
		gomega.Expect(packReport.Freshness.Scope).To(gomega.Equal("pkg/target.go"), fmt.Sprintf("expected required edit path to bound freshness, got %#v", packReport.Freshness))
		gomega.Expect(packReport.CandidateFiles).To(gomega.HaveLen(1), fmt.Sprintf("expected required edit path to outrank noisy task text, got %#v", packReport.CandidateFiles))
		gomega.Expect(packReport.CandidateFiles[0].Path).To(gomega.Equal("pkg/target.go"), fmt.Sprintf("expected required edit path to outrank noisy task text, got %#v", packReport.CandidateFiles))
		gomega.Expect(slices.ContainsFunc(packReport.CandidateFiles[0].RankerReasons, func(reason comprehension.RankerReason) bool {
			return reason.Reason == "required_edit_path"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected required_edit_path ranker reason, got %#v", packReport.CandidateFiles[0].RankerReasons))

	})
	ginkgo.It("context status refreshes bounded stale scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statusbounded\n\ngo 1.22\n")

		sourcePath := filepath.Join(root, "pkg", "pkg.go")
		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Path: "pkg", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			Fresh       bool     `json:"fresh"`
			StaleFiles  []string `json:"stale_files"`
			AutoRefresh struct {
				Attempted            bool     `json:"attempted"`
				Scope                string   `json:"scope"`
				Reason               string   `json:"reason"`
				FilesIndexed         int      `json:"files_indexed"`
				PartialRefreshReason string   `json:"partial_refresh_reason"`
				FullRebuildReason    string   `json:"full_rebuild_reason"`
				FreshAfter           bool     `json:"fresh_after"`
				RefreshedFiles       []string `json:"refreshed_files"`
			} `json:"auto_refresh"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.AutoRefresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected bounded stale-scope status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Scope).To(gomega.Equal("pkg"), fmt.Sprintf("expected bounded stale-scope status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Reason).To(gomega.Equal("stale_scope_files"), fmt.Sprintf("expected bounded stale-scope status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FilesIndexed).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected bounded stale-scope status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FreshAfter).To(gomega.BeTrue(), fmt.Sprintf("expected bounded stale-scope status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected fresh status after bounded refresh, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFiles).To(gomega.BeEmpty(), fmt.Sprintf("expected fresh status after bounded refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.RefreshedFiles).To(gomega.ContainElement("pkg/pkg.go"), fmt.Sprintf("expected fresh status after bounded refresh, got %#v", statusReport))

	})
	ginkgo.It("context status refreshes single file with dependency aware patch", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statussinglefile\n\ngo 1.22\n")

		sourcePath := filepath.Join(root, "pkg", "a.go")
		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		var initialIndex struct {
			IndexID string `json:"index_id"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &initialIndex)).To(gomega.Succeed())

		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Path: "pkg/a.go", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			IndexID     string   `json:"index_id"`
			Fresh       bool     `json:"fresh"`
			StaleFiles  []string `json:"stale_files"`
			AutoRefresh struct {
				Attempted            bool     `json:"attempted"`
				Scope                string   `json:"scope"`
				Reason               string   `json:"reason"`
				FilesIndexed         int      `json:"files_indexed"`
				PartialRefreshReason string   `json:"partial_refresh_reason"`
				FreshAfter           bool     `json:"fresh_after"`
				RefreshedFiles       []string `json:"refreshed_files"`
			} `json:"auto_refresh"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.AutoRefresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected dependency-aware status refresh patch, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Scope).To(gomega.Equal("pkg/a.go"), fmt.Sprintf("expected dependency-aware status refresh patch, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FilesIndexed).To(gomega.BeNumerically("==", 2), fmt.Sprintf("expected dependency-aware status refresh patch, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.PartialRefreshReason).To(gomega.Equal("sqlite_scope_patch"), fmt.Sprintf("expected dependency-aware status refresh patch, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FreshAfter).To(gomega.BeTrue(), fmt.Sprintf("expected dependency-aware status refresh patch, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected fresh status after exact single-file refresh, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFiles).To(gomega.BeEmpty(), fmt.Sprintf("expected fresh status after exact single-file refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.RefreshedFiles).To(gomega.ContainElement("pkg/a.go"), fmt.Sprintf("expected fresh status after exact single-file refresh, got %#v", statusReport))
		gomega.Expect(statusReport.IndexID).To(gomega.Equal(initialIndex.IndexID), fmt.Sprintf("dependency-aware patch should preserve the active graph index id, got before=%s after=%s", initialIndex.IndexID, statusReport.IndexID))

	})
	ginkgo.It("context status refreshes small stale file set without scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statussmall\n\ngo 1.22\n")

		sourcePath := filepath.Join(root, "pkg", "pkg.go")
		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			Fresh       bool     `json:"fresh"`
			StaleFiles  []string `json:"stale_files"`
			AutoRefresh struct {
				Attempted            bool     `json:"attempted"`
				Scope                string   `json:"scope"`
				Reason               string   `json:"reason"`
				FilesIndexed         int      `json:"files_indexed"`
				PartialRefreshReason string   `json:"partial_refresh_reason"`
				FullRebuildReason    string   `json:"full_rebuild_reason"`
				FreshAfter           bool     `json:"fresh_after"`
				RefreshedFiles       []string `json:"refreshed_files"`
			} `json:"auto_refresh"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.AutoRefresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected small-stale-set status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Reason).To(gomega.Equal("small_stale_file_set"), fmt.Sprintf("expected small-stale-set status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FilesIndexed).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected small-stale-set status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FreshAfter).To(gomega.BeTrue(), fmt.Sprintf("expected small-stale-set status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.PartialRefreshReason).To(gomega.Equal("exact_scope_refresh"), fmt.Sprintf("expected unscoped small stale-set repair to stay exact and bounded, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FullRebuildReason).To(gomega.BeEmpty(), fmt.Sprintf("expected unscoped small stale-set repair to stay exact and bounded, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected fresh status after small stale-file refresh, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFiles).To(gomega.BeEmpty(), fmt.Sprintf("expected fresh status after small stale-file refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.RefreshedFiles).To(gomega.ContainElement("pkg/pkg.go"), fmt.Sprintf("expected fresh status after small stale-file refresh, got %#v", statusReport))

	})
	ginkgo.It("context status refresh max files uses default budget for unscoped status", func() {
		staleStatus := comprehension.StatusReport{Indexed: true, Fresh: false}

		if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status"}, staleStatus); got != defaultContextStatusUnboundedRefreshMaxFiles {
			ginkgo.Fail(fmt.Sprintf("expected default unscoped refresh budget %d, got %d", defaultContextStatusUnboundedRefreshMaxFiles, got))
		}

		if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status", Limit: 12}, staleStatus); got != 12 {
			ginkgo.Fail(fmt.Sprintf("expected limit to win over default unscoped refresh budget, got %d", got))
		}

		if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status", FreshnessMaxFiles: 7, Limit: 12}, staleStatus); got != 7 {
			ginkgo.Fail(fmt.Sprintf("expected freshness_max_files to win over limit, got %d", got))
		}

		if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status", Path: "pkg"}, staleStatus); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected bounded path refresh to remain uncapped by default, got %d", got))
		}

		if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status", RequiredEditPaths: []string{"pkg/a.go"}}, staleStatus); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected required edit path refresh to remain uncapped by default, got %d", got))
		}

		if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status"}, comprehension.StatusReport{Indexed: true, Fresh: false, CollectorVersionMismatch: true}); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected collector-version rebuild path to remain uncapped by stale-file default, got %d", got))
		}

		if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status"}, comprehension.StatusReport{Indexed: false, Fresh: false}); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected missing-index rebuild path to remain uncapped by stale-file default, got %d", got))
		}

		if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status"}, comprehension.StatusReport{Indexed: true, Fresh: true}); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected fresh status to need no refresh budget, got %d", got))
		}
	})
	ginkgo.It("context status refresh default budget blocks large unbounded stale set", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statusdefaultbudget\n\ngo 1.22\n")

		const staleFileCount = defaultContextStatusUnboundedRefreshMaxFiles + 5

		for i := 0; i < staleFileCount; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"file_%03d.go", i,
				)), fmt.Sprintf("package pkg\n\nfunc File%03d() string { return \"old\" }\n",

				i),
			)
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		for i := 0; i < staleFileCount; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"file_%03d.go", i,
				)), fmt.Sprintf("package pkg\n\nfunc File%03d() string { return \"new\" }\n",

				i),
			)
		}

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			Fresh          bool     `json:"fresh"`
			StaleFileCount int      `json:"stale_file_count"`
			StaleFiles     []string `json:"stale_files"`
			AutoRefresh    struct {
				Attempted      bool     `json:"attempted"`
				Reason         string   `json:"reason"`
				SkippedReason  string   `json:"skipped_reason"`
				FilesIndexed   int      `json:"files_indexed"`
				FreshAfter     bool     `json:"fresh_after"`
				StalePathCount int      `json:"stale_path_count"`
				MaxFiles       int      `json:"max_files"`
				RefreshScopes  []string `json:"refresh_scopes"`
			} `json:"auto_refresh"`
			Freshness struct {
				FreshForScope  bool `json:"fresh_for_scope"`
				BudgetExceeded bool `json:"budget_exceeded"`
			} `json:"freshness"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Fresh).To(gomega.BeFalse(), fmt.Sprintf("expected stale status with capped stale-file sample, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFileCount).To(gomega.Equal(staleFileCount), fmt.Sprintf("expected stale status with capped stale-file sample, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFiles).To(gomega.HaveLen(defaultContextStatusOutputLimit), fmt.Sprintf("expected stale status with capped stale-file sample, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.Attempted).To(gomega.BeFalse(), fmt.Sprintf("expected default-budget blocker without refresh attempt, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Reason).To(gomega.Equal("budget_exceeded"), fmt.Sprintf("expected default-budget blocker without refresh attempt, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.SkippedReason).To(gomega.Equal("budget_exceeded"), fmt.Sprintf("expected default-budget blocker without refresh attempt, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FilesIndexed).To(gomega.BeNumerically("==", 0), fmt.Sprintf("expected no refresh to run after default-budget blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FreshAfter).To(gomega.BeFalse(), fmt.Sprintf("expected no refresh to run after default-budget blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.StalePathCount).To(gomega.Equal(staleFileCount), fmt.Sprintf("expected default-budget fields on blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.MaxFiles).To(gomega.Equal(defaultContextStatusUnboundedRefreshMaxFiles), fmt.Sprintf("expected default-budget fields on blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.RefreshScopes).To(gomega.BeEmpty(), fmt.Sprintf("expected no refresh scopes after default-budget blocker, got %#v", statusReport.AutoRefresh.RefreshScopes))
		gomega.Expect(statusReport.Freshness.FreshForScope).To(gomega.BeFalse(), fmt.Sprintf("expected freshness budget blocker, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.BudgetExceeded).To(gomega.BeTrue(), fmt.Sprintf("expected freshness budget blocker, got %#v", statusReport.Freshness))

	})
	ginkgo.It("context status refresh limit caps unbounded stale set", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statuslimit\n\ngo 1.22\n")

		for i := 0; i < 3; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"file_%02d.go", i,
				)), fmt.Sprintf("package pkg\n\nfunc File%02d() string { return \"old\" }\n",

				i),
			)
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		for i := 0; i < 3; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"file_%02d.go", i,
				)), fmt.Sprintf("package pkg\n\nfunc File%02d() string { return \"new\" }\n",

				i),
			)
		}

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go", Limit: 2})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status should return a freshness report, not a tool error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			Fresh          bool     `json:"fresh"`
			StalePathCount int      `json:"stale_path_count"`
			StaleFileCount int      `json:"stale_file_count"`
			StaleFiles     []string `json:"stale_files"`
			AutoRefresh    struct {
				Attempted      bool   `json:"attempted"`
				Reason         string `json:"reason"`
				SkippedReason  string `json:"skipped_reason"`
				StalePathCount int    `json:"stale_path_count"`
				MaxFiles       int    `json:"max_files"`
			} `json:"auto_refresh"`
			Freshness struct {
				FreshForScope  bool     `json:"fresh_for_scope"`
				Reason         string   `json:"reason"`
				Blocker        string   `json:"blocker"`
				BudgetExceeded bool     `json:"budget_exceeded"`
				StalePathCount int      `json:"stale_path_count"`
				StalePaths     []string `json:"stale_paths"`
			} `json:"freshness"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Fresh).To(gomega.BeFalse(), fmt.Sprintf("expected stale unbounded status to remain reported, got %#v", statusReport))
		gomega.Expect(statusReport.StalePathCount).To(gomega.BeNumerically("==", 3), fmt.Sprintf("expected stale unbounded status to remain reported, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFileCount).To(gomega.BeNumerically("==", 3), fmt.Sprintf("expected --limit to cap stale file display while preserving counts, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFiles).To(gomega.HaveLen(2), fmt.Sprintf("expected --limit to cap stale file display while preserving counts, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.Attempted).To(gomega.BeFalse(), fmt.Sprintf("expected limit-driven budget blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Reason).To(gomega.Equal("budget_exceeded"), fmt.Sprintf("expected limit-driven budget blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.SkippedReason).To(gomega.Equal("budget_exceeded"), fmt.Sprintf("expected limit-driven budget blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.StalePathCount).To(gomega.BeNumerically("==", 3), fmt.Sprintf("expected limit-driven budget blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.MaxFiles).To(gomega.BeNumerically("==", 2), fmt.Sprintf("expected limit-driven budget blocker, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.Freshness.FreshForScope).To(gomega.BeFalse(), fmt.Sprintf("expected freshness budget blocker, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.BudgetExceeded).To(gomega.BeTrue(), fmt.Sprintf("expected freshness budget blocker, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.Blocker).To(gomega.Equal("budget_exceeded"), fmt.Sprintf("expected freshness budget blocker, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.StalePathCount).To(gomega.BeNumerically("==", 3), fmt.Sprintf("expected --limit to cap freshness stale path display while preserving counts, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.StalePaths).To(gomega.HaveLen(2), fmt.Sprintf("expected --limit to cap freshness stale path display while preserving counts, got %#v", statusReport.Freshness))

	})
	ginkgo.It("context status default caps stale and missing display without losing counts", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statusdisplay\n\ngo 1.22\n")

		for i := 0; i < defaultContextStatusOutputLimit+3; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"stale_%02d.go",
					i)), fmt.Sprintf("package pkg\n\nfunc Stale%02d() string { return \"old\" }\n",

				i),
			)
		}

		for i := 0; i < defaultContextStatusOutputLimit+2; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"missing_%02d.go",
					i)), fmt.Sprintf("package pkg\n\nfunc Missing%02d() string { return \"old\" }\n",

				i),
			)
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))

		for i := 0; i < defaultContextStatusOutputLimit+3; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"stale_%02d.go",
					i)), fmt.Sprintf("package pkg\n\nfunc Stale%02d() string { return \"new\" }\n",

				i),
			)
		}

		for i := 0; i < defaultContextStatusOutputLimit+2; i++ {
			gomega.Expect(os.Remove(filepath.Join(root, "pkg", fmt.Sprintf("missing_%02d.go", i)))).To(gomega.Succeed())

		}

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			StaleFileCount   int      `json:"stale_file_count"`
			MissingFileCount int      `json:"missing_file_count"`
			StalePathCount   int      `json:"stale_path_count"`
			StaleFiles       []string `json:"stale_files"`
			MissingFiles     []string `json:"missing_files"`
			Freshness        struct {
				StalePathCount   int      `json:"stale_path_count"`
				MissingPathCount int      `json:"missing_path_count"`
				StalePaths       []string `json:"stale_paths"`
				MissingPaths     []string `json:"missing_paths"`
			} `json:"freshness"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.StaleFileCount).To(gomega.Equal(defaultContextStatusOutputLimit+3), fmt.Sprintf("expected full stale counts to survive display cap, got %#v", statusReport))
		gomega.Expect(statusReport.MissingFileCount).To(gomega.Equal(defaultContextStatusOutputLimit+2), fmt.Sprintf("expected full stale counts to survive display cap, got %#v", statusReport))
		gomega.Expect(statusReport.StalePathCount).To(gomega.Equal((defaultContextStatusOutputLimit*2)+5), fmt.Sprintf("expected full stale counts to survive display cap, got %#v", statusReport))
		gomega.Expect(statusReport.StaleFiles).To(gomega.HaveLen(defaultContextStatusOutputLimit), fmt.Sprintf("expected default stale file display cap, got %d file(s)", len(statusReport.StaleFiles)))
		gomega.Expect(statusReport.MissingFiles).To(gomega.HaveLen(defaultContextStatusOutputLimit), fmt.Sprintf("expected default missing file display cap, got %d file(s)", len(statusReport.MissingFiles)))
		gomega.Expect(statusReport.Freshness.StalePathCount).To(gomega.Equal(defaultContextStatusOutputLimit+3), fmt.Sprintf("expected default freshness stale path display cap with full count, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.StalePaths).To(gomega.HaveLen(defaultContextStatusOutputLimit), fmt.Sprintf("expected default freshness stale path display cap with full count, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.MissingPathCount).To(gomega.Equal(defaultContextStatusOutputLimit+2), fmt.Sprintf("expected default freshness missing path display cap with full count, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.MissingPaths).To(gomega.HaveLen(defaultContextStatusOutputLimit), fmt.Sprintf("expected default freshness missing path display cap with full count, got %#v", statusReport.Freshness))

	})
	ginkgo.It("context status refresh builds missing root index without scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statusmissingroot\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			Fresh       bool `json:"fresh"`
			Indexed     bool `json:"indexed"`
			AutoRefresh struct {
				Attempted    bool   `json:"attempted"`
				Scope        string `json:"scope"`
				Reason       string `json:"reason"`
				FilesIndexed int    `json:"files_indexed"`
				FreshAfter   bool   `json:"fresh_after"`
			} `json:"auto_refresh"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Indexed).To(gomega.BeTrue(), fmt.Sprintf("expected missing root index refresh, got %#v", statusReport))
		gomega.Expect(statusReport.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected missing root index refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected missing root index refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.Scope).To(gomega.Equal("."), fmt.Sprintf("expected missing root index refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.Reason).To(gomega.Equal("missing_index"), fmt.Sprintf("expected missing root index refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.FilesIndexed).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected missing root index refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.FreshAfter).To(gomega.BeTrue(), fmt.Sprintf("expected missing root index refresh, got %#v", statusReport))

	})
	ginkgo.It("context status refresh builds large missing root index", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statusmissinglarge\n\ngo 1.22\n")

		for i := 0; i < 30; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"file_%02d.go", i,
				)), fmt.Sprintf("package pkg\n\nfunc File%02d() {}\n",

				i),
			)
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status should return a freshness blocker report, not a tool timeout/error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			Indexed     bool `json:"indexed"`
			Fresh       bool `json:"fresh"`
			AutoRefresh struct {
				Attempted    bool   `json:"attempted"`
				Scope        string `json:"scope"`
				Reason       string `json:"reason"`
				FilesIndexed int    `json:"files_indexed"`
				FreshAfter   bool   `json:"fresh_after"`
			} `json:"auto_refresh"`
			Freshness struct {
				FreshForScope  bool `json:"fresh_for_scope"`
				BudgetExceeded bool `json:"budget_exceeded"`
			} `json:"freshness"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Indexed).To(gomega.BeTrue(), fmt.Sprintf("large missing index should build a fresh root graph, got %#v", statusReport))
		gomega.Expect(statusReport.Fresh).To(gomega.BeTrue(), fmt.Sprintf("large missing index should build a fresh root graph, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected large missing-index refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Scope).To(gomega.Equal("."), fmt.Sprintf("expected large missing-index refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Reason).To(gomega.Equal("missing_index"), fmt.Sprintf("expected large missing-index refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FilesIndexed).To(gomega.BeNumerically("==", 30), fmt.Sprintf("expected large missing-index refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FreshAfter).To(gomega.BeTrue(), fmt.Sprintf("expected large missing-index refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.Freshness.FreshForScope).To(gomega.BeTrue(), fmt.Sprintf("expected fresh read-through decision without budget blocker, got %#v", statusReport.Freshness))
		gomega.Expect(statusReport.Freshness.BudgetExceeded).To(gomega.BeFalse(), fmt.Sprintf("expected fresh read-through decision without budget blocker, got %#v", statusReport.Freshness))

	})
	ginkgo.It("context status refresh rebuilds collector mismatch without scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statuscollectors\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		decision := mgr.contextStatusFreshness(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"}, comprehension.StatusReport{
			Indexed:                  true,
			CollectorVersionMismatch: true,
		})
		refresh := decision.Refresh
		gomega.Expect(refresh).NotTo(gomega.BeNil(), fmt.Sprintf("expected collector mismatch root rebuild, got %#v", refresh))
		gomega.Expect(refresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected collector mismatch root rebuild, got %#v", refresh))
		gomega.Expect(refresh.Scope).To(gomega.Equal("."), fmt.Sprintf("expected collector mismatch root rebuild, got %#v", refresh))
		gomega.Expect(refresh.Reason).To(gomega.Equal("collector_version_mismatch"), fmt.Sprintf("expected collector mismatch root rebuild, got %#v", refresh))
		gomega.Expect(refresh.FilesIndexed).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected collector mismatch root rebuild, got %#v", refresh))
		gomega.Expect(refresh.FreshAfter).To(gomega.BeTrue(), fmt.Sprintf("expected collector mismatch root rebuild, got %#v", refresh))

		status, err := comprehension.Status(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("context status after refresh: %v", err))
		gomega.Expect(status.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected fresh graph after collector mismatch rebuild, got %#v", status))
		gomega.Expect(status.CollectorVersionMismatch).To(gomega.BeFalse(), fmt.Sprintf("expected fresh graph after collector mismatch rebuild, got %#v", status))

	})
	ginkgo.It("context status refresh removes small missing file set without scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/statusmissing\n\ngo 1.22\n")

		missingPath := filepath.Join(root, "pkg", "gone.go")
		writeWorkspaceFile(missingPath, "package pkg\n\nfunc Gone() string { return \"gone\" }\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "keep.go"), "package pkg\n\nfunc Keep() string { return \"keep\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(os.Remove(missingPath)).To(gomega.Succeed())

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_status returned error: %v\n%s", result.Error, result.Output))

		var statusReport struct {
			Fresh        bool     `json:"fresh"`
			MissingFiles []string `json:"missing_files"`
			AutoRefresh  struct {
				Attempted      bool     `json:"attempted"`
				Reason         string   `json:"reason"`
				FreshAfter     bool     `json:"fresh_after"`
				RefreshedFiles []string `json:"refreshed_files"`
			} `json:"auto_refresh"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.AutoRefresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected capped missing-file status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.Reason).To(gomega.Equal("small_stale_file_set"), fmt.Sprintf("expected capped missing-file status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.AutoRefresh.FreshAfter).To(gomega.BeTrue(), fmt.Sprintf("expected capped missing-file status refresh, got %#v", statusReport.AutoRefresh))
		gomega.Expect(statusReport.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected fresh status after missing-file refresh, got %#v", statusReport))
		gomega.Expect(statusReport.MissingFiles).To(gomega.BeEmpty(), fmt.Sprintf("expected fresh status after missing-file refresh, got %#v", statusReport))
		gomega.Expect(statusReport.AutoRefresh.RefreshedFiles).To(gomega.ContainElement("pkg/gone.go"), fmt.Sprintf("expected fresh status after missing-file refresh, got %#v", statusReport))

	})
	ginkgo.It("context query auto refreshes missing bounded index", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/autorefresh-query\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_query", Pattern: "Run", Path: "pkg", Language: "go", Resolution: "symbol"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_query returned error: %v\n%s", result.Error, result.Output))

		var queryReport struct {
			AutoRefresh struct {
				Attempted    bool   `json:"attempted"`
				Scope        string `json:"scope"`
				Reason       string `json:"reason"`
				FilesIndexed int    `json:"files_indexed"`
			} `json:"auto_refresh"`
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &queryReport)).To(gomega.Succeed())
		gomega.Expect(queryReport.AutoRefresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected missing-index auto-refresh, got %#v", queryReport.AutoRefresh))
		gomega.Expect(queryReport.AutoRefresh.Scope).To(gomega.Equal("pkg"), fmt.Sprintf("expected missing-index auto-refresh, got %#v", queryReport.AutoRefresh))
		gomega.Expect(queryReport.AutoRefresh.Reason).To(gomega.Equal("missing_index"), fmt.Sprintf("expected missing-index auto-refresh, got %#v", queryReport.AutoRefresh))
		gomega.Expect(queryReport.AutoRefresh.FilesIndexed).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected missing-index auto-refresh, got %#v", queryReport.AutoRefresh))
		gomega.Expect(slices.ContainsFunc(queryReport.Nodes, func(node struct {
			ID string `json:"id"`
		}) bool {

			return node.ID == "symbol://pkg/pkg.go#Run"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected Run node after auto-refresh, got %#v", queryReport.Nodes))

	})
	ginkgo.It("context query refreshes unbounded stale graph without budget blocker", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/querylarge\n\ngo 1.22\n")

		for i := 0; i < 30; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"f%03d.go", i)), fmt.Sprintf("package pkg\n\nfunc F%03d() string { return \"old\" }\n",

				i),
			)
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		for i := 0; i < 30; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"f%03d.go", i)), fmt.Sprintf("package pkg\n\nfunc F%03d() string { return \"new\" }\n",

				i),
			)
		}

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_query", Query: "what functions changed", Language: "go", Resolution: "symbol"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_query should refresh large stale scopes instead of returning a budget blocker, got error=%q output=%s", result.Error, result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(`"budget_exceeded":true`), fmt.Sprintf("expected no budget freshness blocker, got %s", result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(`"error":"freshness_blocker"`), fmt.Sprintf("expected no budget freshness blocker, got %s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`"reason":"stale_unbounded_graph"`), fmt.Sprintf("expected stale unbounded graph refresh evidence, got %s", result.Output))

	})
	ginkgo.It("context query stale evidence blocker and report only modes", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/staleevidence\n\ngo 1.22\n")

		for i := 0; i < 3; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"f%d.go", i)), fmt.Sprintf("package pkg\n\nfunc Old%d() string { return \"old\" }\n",

				i),
			)
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		for i := 0; i < 3; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"f%d.go", i)), fmt.Sprintf("package pkg\n\nfunc New%d() string { return \"new\" }\n",

				i),
			)
		}

		blocked := mgr.Execute(context.Background(), ToolRequest{
			Tool:              "context_query",
			Query:             "Old",
			Language:          "go",
			Resolution:        "symbol",
			FreshnessPolicy:   "require_fresh",
			FreshnessMaxFiles: 1,
		})
		gomega.Expect(blocked.Error).To(gomega.Equal("freshness_blocker"), fmt.Sprintf("expected freshness blocker, got error=%q output=%s", blocked.Error, blocked.Output))

		var blockerReport struct {
			Error         string                            `json:"error"`
			Freshness     comprehension.FreshnessDecision   `json:"freshness"`
			StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
		}
		gomega.Expect(json.Unmarshal([]byte(blocked.Output), &blockerReport)).To(gomega.Succeed())
		gomega.Expect(blockerReport.StaleEvidence.State).To(gomega.Equal("blocked"), fmt.Sprintf("expected blocked stale-evidence state, got %#v", blockerReport))
		gomega.Expect(blockerReport.StaleEvidence.Policy).To(gomega.Equal(comprehension.FreshnessRequire), fmt.Sprintf("expected blocked stale-evidence state, got %#v", blockerReport))
		gomega.Expect(blockerReport.Freshness.BudgetExceeded).To(gomega.BeTrue(), fmt.Sprintf("expected blocked stale-evidence state, got %#v", blockerReport))

		reported := mgr.Execute(context.Background(), ToolRequest{
			Tool:              "context_query",
			Query:             "Old",
			Language:          "go",
			Resolution:        "symbol",
			FreshnessPolicy:   "report_only",
			FreshnessMaxFiles: 1,
		})
		gomega.Expect(reported.Error).To(gomega.Equal(""), fmt.Sprintf("report-only context_query should not hard block, got error=%q output=%s", reported.Error, reported.Output))

		var reportOnly struct {
			Fresh         bool                              `json:"fresh"`
			Freshness     comprehension.FreshnessDecision   `json:"freshness"`
			StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
			Confidence    struct {
				Level  string `json:"level"`
				Reason string `json:"reason"`
			} `json:"confidence"`
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}
		gomega.Expect(json.Unmarshal([]byte(reported.Output), &reportOnly)).To(gomega.Succeed())
		gomega.Expect(reportOnly.Fresh).To(gomega.BeFalse(), fmt.Sprintf("expected report-only stale evidence with low confidence, got %#v", reportOnly))
		gomega.Expect(reportOnly.Freshness.Policy).To(gomega.Equal(comprehension.FreshnessReportOnly), fmt.Sprintf("expected report-only stale evidence with low confidence, got %#v", reportOnly))
		gomega.Expect(reportOnly.StaleEvidence.State).To(gomega.Equal("reported"), fmt.Sprintf("expected report-only stale evidence with low confidence, got %#v", reportOnly))
		gomega.Expect(reportOnly.Confidence.Level).To(gomega.Equal("low"), fmt.Sprintf("expected report-only stale evidence with low confidence, got %#v", reportOnly))
		gomega.Expect(reportOnly.Nodes).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected stale graph nodes to remain observable in report-only mode, got %#v", reportOnly))

		fields := ContextTelemetryFields("context_query", reported.Output)
		gomega.Expect(fields["context_stale_evidence_state"]).To(gomega.Equal("reported"), fmt.Sprintf("expected stale-use report telemetry, got %#v", fields))
		gomega.Expect(fields["context_stale_use_reported"]).To(gomega.BeTrue(), fmt.Sprintf("expected stale-use report telemetry, got %#v", fields))
		gomega.Expect(fields["context_freshness_state"]).To(gomega.Equal("reported"), fmt.Sprintf("expected stale-use report telemetry, got %#v", fields))
		gomega.Expect(fields["context_freshness_blocked"]).NotTo(gomega.Equal(true), fmt.Sprintf("expected stale-use report telemetry, got %#v", fields))

	})
	ginkgo.It("context zoom preserves freshness policy and max files", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/zoompolicy\n\ngo 1.22\n")

		for i := 0; i < 3; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"f%d.go", i)), fmt.Sprintf("package pkg\n\nfunc Old%d() string { return \"old\" }\n",

				i),
			)
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		for i := 0; i < 3; i++ {
			writeWorkspaceFile(filepath.Join(root,

				"pkg", fmt.Sprintf(
					"f%d.go", i)), fmt.Sprintf("package pkg\n\nfunc New%d() string { return \"new\" }\n",

				i),
			)
		}

		blocked := mgr.Execute(context.Background(), ToolRequest{
			Tool:              "context_zoom",
			Handle:            "symbol://pkg/f0.go#Old0",
			Path:              "pkg",
			Language:          "go",
			FreshnessPolicy:   "require_fresh",
			FreshnessMaxFiles: 1,
		})
		gomega.Expect(blocked.Error).To(gomega.Equal("freshness_blocker"), fmt.Sprintf("expected require_fresh zoom budget blocker, got error=%q output=%s", blocked.Error, blocked.Output))

		var blockerReport struct {
			Freshness     comprehension.FreshnessDecision   `json:"freshness"`
			StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
		}
		gomega.Expect(json.Unmarshal([]byte(blocked.Output), &blockerReport)).To(gomega.Succeed())
		gomega.Expect(blockerReport.Freshness.Policy).To(gomega.Equal(comprehension.FreshnessRequire), fmt.Sprintf("expected propagated require_fresh policy and max-file budget, got %#v", blockerReport))
		gomega.Expect(blockerReport.Freshness.BudgetExceeded).To(gomega.BeTrue(), fmt.Sprintf("expected propagated require_fresh policy and max-file budget, got %#v", blockerReport))
		gomega.Expect(blockerReport.StaleEvidence.State).To(gomega.Equal("blocked"), fmt.Sprintf("expected propagated require_fresh policy and max-file budget, got %#v", blockerReport))

		reported := mgr.Execute(context.Background(), ToolRequest{
			Tool:              "context_zoom",
			Handle:            "symbol://pkg/f0.go#Old0",
			Path:              "pkg",
			Language:          "go",
			FreshnessPolicy:   "report_only",
			FreshnessMaxFiles: 1,
		})
		gomega.Expect(reported.Error).To(gomega.Equal(""), fmt.Sprintf("report-only context_zoom should not hard block, got error=%q output=%s", reported.Error, reported.Output))

		var zoomReport struct {
			Freshness     comprehension.FreshnessDecision   `json:"freshness"`
			StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
		}
		gomega.Expect(json.Unmarshal([]byte(reported.Output), &zoomReport)).To(gomega.Succeed())
		gomega.Expect(zoomReport.Freshness.Policy).To(gomega.Equal(comprehension.FreshnessReportOnly), fmt.Sprintf("expected report-only policy to remain report-only, got %#v", zoomReport))
		gomega.Expect(zoomReport.StaleEvidence.State).To(gomega.Equal("reported"), fmt.Sprintf("expected report-only policy to remain report-only, got %#v", zoomReport))
		gomega.Expect(zoomReport.Freshness.Refresh).To(gomega.BeNil(), fmt.Sprintf("expected report-only policy to remain report-only, got %#v", zoomReport))

		assertNoPrescriptiveGraphOutput("context_zoom report-only stale output", reported.Output)

		fields := ContextTelemetryFields("context_zoom", reported.Output)
		gomega.Expect(fields["context_freshness_state"]).To(gomega.Equal("reported"), fmt.Sprintf("expected report-only zoom telemetry without hard blocker, got %#v", fields))
		gomega.Expect(fields["context_freshness_blocked"]).NotTo(gomega.Equal(true), fmt.Sprintf("expected report-only zoom telemetry without hard blocker, got %#v", fields))
		gomega.Expect(fields["context_stale_use_reported"]).To(gomega.BeTrue(), fmt.Sprintf("expected report-only zoom telemetry without hard blocker, got %#v", fields))

	})
	ginkgo.It("context zoom refreshes handle file before returning graph facts", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/zoomfresh\n\ngo 1.22\n")

		sourcePath := filepath.Join(root, "pkg", "pkg.go")
		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"old\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		writeWorkspaceFile(sourcePath, "package pkg\n\nfunc Run() string { return \"new\" }\n")

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_zoom", ID: "symbol://pkg/pkg.go#Run", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_zoom returned error: %v\n%s", result.Error, result.Output))

		var zoom struct {
			Freshness *comprehension.FreshnessDecision `json:"freshness"`
			Node      *struct {
				ID string `json:"id"`
			} `json:"node"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &zoom)).To(gomega.Succeed())
		gomega.Expect(zoom.Node).NotTo(gomega.BeNil(), fmt.Sprintf("expected refreshed Run node, got %#v", zoom.Node))
		gomega.Expect(zoom.Node.ID).To(gomega.Equal("symbol://pkg/pkg.go#Run"), fmt.Sprintf("expected refreshed Run node, got %#v", zoom.Node))
		gomega.Expect(zoom.Freshness).NotTo(gomega.BeNil(), fmt.Sprintf("expected context_zoom freshness refresh, got %#v", zoom.Freshness))
		gomega.Expect(zoom.Freshness.FreshForScope).To(gomega.BeTrue(), fmt.Sprintf("expected context_zoom freshness refresh, got %#v", zoom.Freshness))
		gomega.Expect(zoom.Freshness.Refresh).NotTo(gomega.BeNil(), fmt.Sprintf("expected context_zoom freshness refresh, got %#v", zoom.Freshness))
		gomega.Expect(zoom.Freshness.Refresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected context_zoom freshness refresh, got %#v", zoom.Freshness))

	})
	ginkgo.It("context zoom accepts handle alias", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/zoomhandle\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_zoom", Handle: "symbol://pkg/pkg.go#Run", Language: "go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_zoom with handle alias returned error: %v\n%s", result.Error, result.Output))

		var zoom struct {
			Node *struct {
				ID string `json:"id"`
			} `json:"node"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &zoom)).To(gomega.Succeed())
		gomega.Expect(zoom.Node).NotTo(gomega.BeNil(), fmt.Sprintf("expected Run node via handle alias, got %#v", zoom.Node))
		gomega.Expect(zoom.Node.ID).To(gomega.Equal("symbol://pkg/pkg.go#Run"), fmt.Sprintf("expected Run node via handle alias, got %#v", zoom.Node))

	})
	ginkgo.It("context zoom include source keeps graph only output", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/zoomsource\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"source body\" }\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:          "context_zoom",
			Handle:        "symbol://pkg/pkg.go#Run",
			Language:      "go",
			IncludeSource: true,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_zoom include_source returned error: %v\n%s", result.Error, result.Output))

		var zoom struct {
			Node *struct {
				ID         string         `json:"id"`
				Path       string         `json:"path"`
				Source     string         `json:"source"`
				SourceBody string         `json:"source_body"`
				Properties map[string]any `json:"properties"`
			} `json:"node"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &zoom)).To(gomega.Succeed())
		gomega.Expect(zoom.Node).NotTo(gomega.BeNil(), fmt.Sprintf("expected graph node for Run, got %#v", zoom.Node))
		gomega.Expect(zoom.Node.ID).To(gomega.Equal("symbol://pkg/pkg.go#Run"), fmt.Sprintf("expected graph node for Run, got %#v", zoom.Node))
		gomega.Expect(zoom.Node.Path).To(gomega.Equal("pkg/pkg.go"), fmt.Sprintf("expected graph node for Run, got %#v", zoom.Node))
		gomega.Expect(zoom.Node.Source).To(gomega.BeEmpty(), fmt.Sprintf("context_zoom include_source currently emits graph metadata, not source body: %#v", zoom.Node))
		gomega.Expect(zoom.Node.SourceBody).To(gomega.BeEmpty(), fmt.Sprintf("context_zoom include_source currently emits graph metadata, not source body: %#v", zoom.Node))

		if _, ok := zoom.Node.Properties["source"]; ok {
			ginkgo.Fail(fmt.Sprintf("context_zoom include_source must not hide source body in node properties: %#v", zoom.Node.Properties))
		}
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("return \"source body\""), fmt.Sprintf("context_zoom include_source unexpectedly emitted source body:\n%s", result.Output))

	})
	ginkgo.It("context trace pack captures failing command as evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "app.py"), "def run():\n    raise AssertionError('bad')\n")

		cfg := config.Default().Workspace

		cfg.Root = root
		cfg.CWD = root

		mgr := NewManager(cfg)

		command := "printf '%s\\n' 'Traceback (most recent call last):' '  File \"" + filepath.ToSlash(filepath.Join(root, "app.py")) + "\", line 2, in run' 'AssertionError: bad' >&2; exit 1"
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_trace_pack", Command: command, Language: "auto"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("context_trace_pack should return evidence for failing commands, got error: %v\n%s", result.Error, result.Output))

		var trace struct {
			Success        bool   `json:"success"`
			ArtifactPath   string `json:"artifact_path"`
			FailingAnchors []struct {
				Path string `json:"path"`
				Line int    `json:"line"`
			} `json:"failing_anchors"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &trace)).To(gomega.Succeed())
		gomega.Expect(trace.Success).To(gomega.BeFalse(), fmt.Sprintf("expected saved failing trace with app.py anchor, got %#v", trace))
		gomega.Expect(trace.ArtifactPath).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected saved failing trace with app.py anchor, got %#v", trace))
		gomega.Expect(slices.ContainsFunc(trace.FailingAnchors, func(anchor struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		}) bool {

			return anchor.Path == "app.py" && anchor.Line == 2
		})).To(gomega.BeTrue(), fmt.Sprintf("expected saved failing trace with app.py anchor, got %#v", trace))

	})
	ginkgo.It("read tool schemas advertise post readiness gap fields", func() {
		defs := BuildToolDefinitions([]string{"search_content", "read_file"})

		for _, want := range []string{"search_content", "read_file"} {
			var matched bool

			for _, def := range defs {
				if def.Name != want {
					continue
				}

				matched = true

				var schema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				}
				gomega.Expect(json.Unmarshal([]byte(def.ParametersJSON), &schema)).To(gomega.Succeed())

				if _, ok := schema.Properties["named_gap"]; !ok {
					ginkgo.Fail(fmt.Sprintf("expected %s schema to advertise named_gap, got %#v", want, schema.Properties))
				}

				if _, ok := schema.Properties["why_not_answering"]; !ok {
					ginkgo.Fail(fmt.Sprintf("expected %s schema to advertise why_not_answering, got %#v", want, schema.Properties))
				}
			}
			gomega.Expect(matched).To(gomega.BeTrue(), fmt.Sprintf("missing tool definition for %s", want))

		}
	})
	ginkgo.It("edit file schema advertises both replacement shapes", func() {
		defs := BuildToolDefinitions([]string{"edit_file"})
		gomega.Expect(defs).To(gomega.HaveLen(1), fmt.Sprintf("expected one edit_file definition, got %#v", defs))

		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		gomega.Expect(json.Unmarshal([]byte(defs[0].ParametersJSON), &schema)).To(gomega.Succeed())

		for _, want := range []string{"path", "old_text", "new_text", "start_line", "end_line", "content", "description"} {
			if _, ok := schema.Properties[want]; !ok {
				ginkgo.Fail(fmt.Sprintf("expected edit_file schema to expose %q, got %#v", want, schema.Properties))
			}
		}

		for _, want := range []string{"path", "description"} {
			gomega.Expect(slices.Contains(schema.Required, want)).To(gomega.BeTrue(), fmt.Sprintf("expected edit_file schema to require %q, got %#v", want, schema.Required))

		}

		for _, invalid := range []string{"content", "old_text", "new_text", "start_line", "end_line"} {
			gomega.Expect(slices.Contains(schema.Required, invalid)).To(gomega.BeFalse(), fmt.Sprintf("edit_file schema should not require shape-specific field %q, got %#v", invalid, schema.Required))

		}
		gomega.Expect(defs[0].Description).To(gomega.ContainSubstring("old_text+new_text"), fmt.Sprintf("edit_file description should name both valid shapes, got %q", defs[0].Description))
		gomega.Expect(defs[0].Description).To(gomega.ContainSubstring("start_line+end_line+content"), fmt.Sprintf("edit_file description should name both valid shapes, got %q", defs[0].Description))

	})
	ginkgo.It("harness audit reports tool surface and language capabilities", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/audit\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "tool_surface"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.ToolSurface).NotTo(gomega.BeNil(), fmt.Sprintf("expected manifest tool count, got %#v", report.ToolSurface))
		gomega.Expect(report.ToolSurface.ManifestToolCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected manifest tool count, got %#v", report.ToolSurface))
		gomega.Expect(report.ToolSurface.ExportedSchemaToolCount).To(gomega.Equal(report.ToolSurface.ManifestToolCount), fmt.Sprintf("expected manifest/exported schema alignment, got %#v", report.ToolSurface))
		gomega.Expect(report.ToolSurface.DriftFindingCount).To(gomega.BeNumerically("==", 0), fmt.Sprintf("expected manifest/exported schema alignment, got %#v", report.ToolSurface))
		gomega.Expect(report.ToolSurface.ManifestWithoutSchema).To(gomega.BeEmpty(), fmt.Sprintf("manifest tools missing schemas: %#v", report.ToolSurface.ManifestWithoutSchema))
		gomega.Expect(report.ToolSurface.SchemaWithoutManifest).To(gomega.BeEmpty(), fmt.Sprintf("schemas missing from built-in tool surfaces: %#v", report.ToolSurface.SchemaWithoutManifest))
		gomega.Expect(slices.Contains(toolManifestToolsAcrossBuiltInSurfaces(), "submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("built-in surface union should include conditional submit_plan"))
		gomega.Expect(slices.ContainsFunc(report.ToolSurface.StaleUseBlockerCoverage, func(row harnessAuditStaleUseCoverage) bool {
			return row.Tool == "context_query" && row.RuntimeBlocker && row.SchemaErrorClass && row.State == "blocked"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected context_query stale-use blocker coverage, got %#v", report.ToolSurface.StaleUseBlockerCoverage))
		gomega.Expect(slices.Contains(report.ToolSurface.StaleUseReportOnlyTools, "context_status")).To(gomega.BeTrue(), fmt.Sprintf("expected context_status report-only stale-use coverage, got %#v", report.ToolSurface.StaleUseReportOnlyTools))
		gomega.Expect(slices.ContainsFunc(report.LanguageTooling, func(lang harnessAuditLanguage) bool {
			return lang.Language == "go" && lang.Active && slices.ContainsFunc(lang.Capabilities, func(tool harnessAuditLanguageTool) bool {
				return tool.Tool == "go_workspace_symbol" && tool.InManifest && tool.HasSchema
			})
		})).To(gomega.BeTrue(), fmt.Sprintf("expected active Go language tooling with go_workspace_symbol, got %#v", report.LanguageTooling))
		gomega.Expect(slices.ContainsFunc(report.BackendAvailability, func(backend harnessAuditBackend) bool {
			return backend.Name == "context_graph_sqlite" && backend.Active && backend.Available && strings.Contains(backend.Path, "graph.sqlite")
		})).To(gomega.BeTrue(), fmt.Sprintf("expected harness audit to report active SQLite context graph backend, got %#v", report.BackendAvailability))

	})
	ginkgo.It("harness audit behavior scans logs", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "conversations", "session.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"message","role":"user","content":"find missing code"}`,
			`{"type":"tool_request","tool":"search_files","action":"search_files:_:Nope:_","raw":"{\"tool\":\"search_files\",\"pattern\":\"Nope\"}"}`,
			`{"type":"tool_result","tool":"search_files","summary":"No file matches for \"Nope\"","error":""}`,
			`{"type":"tool_request","tool":"code_navigate","action":"code_navigate:read_symbol:a.go:main","raw":"{\"tool\":\"code_navigate\",\"path\":\"a.go\",\"symbol\":\"main\",\"capability\":\"read_symbol\"}"}`,
			`{"type":"tool_result","tool":"code_navigate","summary":"Read symbol main","error":""}`,
			"",
		}, "\n"))
		writeWorkspaceFile(filepath.Join(root, "atom.md"), "# atom\n")
		writeWorkspaceFile(filepath.Join(root, ".idiom", "lane-history.jsonl"), strings.Join([]string{
			`{"ts":"2026-05-13T00:00:00Z","mode":"plan-execute","brief":"` + filepath.ToSlash(filepath.Join(root, "atom.md")) + `","exit_code":1,"final_state":"stale-needs-rebase","model_calls":0,"tool_calls":0,"total_tokens":0,"duration_ms":0}`,
			`{"ts":"2026-05-13T00:01:00Z","mode":"plan-execute","brief":"atom-2.md","exit_code":0,"final_state":"success","model_calls":2,"tool_calls":3,"total_tokens":1200,"duration_ms":4500}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected one scanned log, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.LogsScanned).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected one scanned log, got %#v", report.LogBehavior))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.TopPatterns, func(hit eval.PerfPatternHit) bool {
			return hit.Name == "zero_hit_search" && hit.TotalCount == 1
		})).To(gomega.BeTrue(), fmt.Sprintf("expected zero_hit_search pattern, got %#v", report.LogBehavior.TopPatterns))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.CapabilityMix, func(row harnessAuditCapabilityMix) bool {
			return row.Capability == "find_references" && row.Calls == 1
		})).To(gomega.BeTrue(), fmt.Sprintf("expected observed behavior capability mix for search_files, got %#v", report.LogBehavior.CapabilityMix))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.CapabilityMix, func(row harnessAuditCapabilityMix) bool {
			return row.Capability == "inspect_symbol" && row.Calls == 1
		})).To(gomega.BeTrue(), fmt.Sprintf("expected observed behavior capability mix for code_navigate read_symbol, got %#v", report.LogBehavior.CapabilityMix))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.CapabilityMix, func(row harnessAuditCapabilityMix) bool {
			return row.Capability == "find_definition" && row.Calls != 0
		})).To(gomega.BeFalse(), fmt.Sprintf("code_navigate read_symbol must not expand to every static code_navigate capability, got %#v", report.LogBehavior.CapabilityMix))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.TopCapabilities, func(row harnessAuditCapabilityMix) bool {
			return row.Capability == "find_references" && row.Calls == 1
		})).To(gomega.BeTrue(), fmt.Sprintf("expected top capability rollup, got %#v", report.LogBehavior.TopCapabilities))
		gomega.Expect(report.LogBehavior.LaneHistory).NotTo(gomega.BeNil(), fmt.Sprintf("expected lane history aggregate, got %#v", report.LogBehavior.LaneHistory))
		gomega.Expect(report.LogBehavior.LaneHistory.Runs).To(gomega.BeNumerically("==", 2), fmt.Sprintf("expected lane history aggregate, got %#v", report.LogBehavior.LaneHistory))
		gomega.Expect(report.LogBehavior.LaneHistory.ByFinalState["stale-needs-rebase"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected lane history aggregate, got %#v", report.LogBehavior.LaneHistory))
		gomega.Expect(report.LogBehavior.LaneHistory.TotalTokens).To(gomega.BeNumerically("==", 1200), fmt.Sprintf("expected lane history aggregate, got %#v", report.LogBehavior.LaneHistory))
		gomega.Expect(report.LogBehavior.LaneHistory.Recent[0].Brief).To(gomega.Equal("atom.md"), fmt.Sprintf("expected lane history aggregate, got %#v", report.LogBehavior.LaneHistory))

	})
	ginkgo.It("harness audit behavior summarizes malformed output stages", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "malformed.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"run_event","role":"tool_request_normalizer","content":"{\"outcome\":\"unsafe_to_infer\",\"parser_failure_class\":\"missing_tool\"}"}`,
			`{"type":"run_event","role":"edit_intent_validated","content":"{\"schema_version\":\"idiom.edit_intent.v1\",\"validation_result\":\"accepted\"}"}`,
			`{"type":"run_event","role":"patch_compile_result","content":"{\"compile_outcome\":\"rejected\",\"failure_class\":\"non_unique_anchor\"}"}`,
			`{"type":"message","role":"malformed_output_recovery_exhausted","content":"{\"recovery_outcome\":\"exhausted\",\"failure_class\":\"parser_failure_budget\"}"}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())

		stages := report.LogBehavior.MalformedOutput
		gomega.Expect(stages).NotTo(gomega.BeNil(), fmt.Sprintf("expected malformed output stage summary, got %#v", report.LogBehavior))
		gomega.Expect(stages.LogsHit).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.EventCounts["tool_request_normalizer"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.EventCounts["edit_intent_validated"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.EventCounts["patch_compile_result"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.EventCounts["malformed_output_recovery_exhausted"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.ValidationResultCounts["accepted"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.FailureClassCounts["non_unique_anchor"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.CompileOutcomeCounts["rejected"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.CompileOutcomeCounts["unsafe_to_infer"]).To(gomega.BeNumerically("==", 0), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.RecoveryOutcomeCounts["exhausted"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(stages.SchemaVersionCounts["idiom.edit_intent.v1"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected malformed output stage summary: %#v", stages))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("old_text"), fmt.Sprintf("malformed output audit should expose counts, not raw payload bodies: %s", result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("raw transcript"), fmt.Sprintf("malformed output audit should expose counts, not raw payload bodies: %s", result.Output))

	})
	ginkgo.It("harness audit behavior redacts worst offender prompts", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "conversations", "session.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"message","role":"user","content":"private customer prompt with secret"}`,
			`{"type":"tool_request","tool":"read_file","action":"read_file:a.go","raw":"{\"tool\":\"read_file\",\"path\":\"a.go\"}"}`,
			`{"type":"tool_result","tool":"read_file","summary":"Read a.go","error":""}`,
			`{"type":"tool_request","tool":"read_file","action":"read_file:a.go","raw":"{\"tool\":\"read_file\",\"path\":\"a.go\"}"}`,
			`{"type":"tool_result","tool":"read_file","summary":"Read a.go","error":""}`,
			`{"type":"tool_request","tool":"read_file","action":"read_file:a.go","raw":"{\"tool\":\"read_file\",\"path\":\"a.go\"}"}`,
			`{"type":"tool_result","tool":"read_file","summary":"Read a.go","error":""}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("user_prompt"), fmt.Sprintf("harness_audit leaked prompt data: %s", result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("private customer prompt"), fmt.Sprintf("harness_audit leaked prompt data: %s", result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("secret"), fmt.Sprintf("harness_audit leaked prompt data: %s", result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected redacted worst offender marker, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.WorstOffenders).To(gomega.HaveLen(1), fmt.Sprintf("expected redacted worst offender marker, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.WorstOffenders[0].LogExcerptRedacted).To(gomega.BeTrue(), fmt.Sprintf("expected redacted worst offender marker, got %#v", report.LogBehavior))

	})
	ginkgo.It("harness feedback returns structured telemetry fields", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:         "harness_feedback",
			Category:     "prompt_confusion",
			Component:    "context_pack",
			Expected:     "packet names the symbol edge needed to choose the edit target",
			Observed:     "packet named both files but not the deciding relationship",
			Workaround:   "used python_references before editing",
			Impact:       "extra read loop before a bounded edit",
			Evidence:     []string{"logs/conversations/session.jsonl step 8"},
			Confidence:   "observed",
			Severity:     "medium",
			SuggestedFix: "carry the top relationship edge into the packet when confidence is high",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_feedback returned error: %v\n%s", result.Error, result.Output))

		var report harnessFeedbackReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.Category).To(gomega.Equal("prompt_gap"), fmt.Sprintf("unexpected normalized feedback report: %#v", report))
		gomega.Expect(report.Confidence).To(gomega.Equal("verified"), fmt.Sprintf("unexpected normalized feedback report: %#v", report))
		gomega.Expect(report.Severity).To(gomega.Equal("medium"), fmt.Sprintf("unexpected normalized feedback report: %#v", report))

		fields := ToolTelemetryFields(result.Tool, result.Output)
		gomega.Expect(fields["feedback_category"]).To(gomega.Equal("prompt_gap"), fmt.Sprintf("unexpected feedback telemetry fields: %#v", fields))
		gomega.Expect(fields["feedback_component"]).To(gomega.Equal("context_pack"), fmt.Sprintf("unexpected feedback telemetry fields: %#v", fields))
		gomega.Expect(fields["feedback_confidence"]).To(gomega.Equal("verified"), fmt.Sprintf("unexpected feedback telemetry fields: %#v", fields))
		gomega.Expect(fields["feedback_severity"]).To(gomega.Equal("medium"), fmt.Sprintf("unexpected feedback telemetry fields: %#v", fields))
		gomega.Expect(fields["feedback_evidence_count"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected feedback telemetry fields: %#v", fields))

	})
	ginkgo.It("harness feedback planning quality feeds audit clusters", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:         "harness_feedback",
			Category:     "impact_ranking",
			Component:    "context_impact",
			Expected:     "ranked scope names the file that owns the planning behavior",
			Observed:     "ranking omitted the owner file even though context_pack had it",
			Impact:       "planner needed extra reads before producing bounded atoms",
			Confidence:   "verified",
			Severity:     "high",
			SuggestedFix: "include context_pack file candidates in impact ranking",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_feedback returned error: %v\n%s", result.Error, result.Output))

		var report harnessFeedbackReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.Category).To(gomega.Equal("impact_ranking"), fmt.Sprintf("expected planning-quality category normalization, got %#v", report))

		fields := ToolTelemetryFields(result.Tool, result.Output)
		logPath := filepath.Join(root, "logs", "telemetry", "feedback.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"tool_request","tool":"harness_feedback","action":"harness_feedback"}`,
			mustMarshalTestJSON(map[string]any{
				"type":                "tool_result",
				"tool":                "harness_feedback",
				"error":               "",
				"summary":             result.Summary,
				"feedback_category":   fields["feedback_category"],
				"feedback_component":  fields["feedback_component"],
				"feedback_confidence": fields["feedback_confidence"],
				"feedback_severity":   fields["feedback_severity"],
			}),
			"",
		}, "\n"))

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "planning_quality", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var audit harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &audit)).To(gomega.Succeed())
		gomega.Expect(audit.PlanningQuality).NotTo(gomega.BeNil(), fmt.Sprintf("expected planning-quality audit cluster, got %#v", audit.PlanningQuality))
		gomega.Expect(audit.PlanningQuality.PlanningFeedback).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected planning-quality audit cluster, got %#v", audit.PlanningQuality))
		gomega.Expect(audit.PlanningQuality.PlanningCategories["impact_ranking"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected planning-quality audit cluster, got %#v", audit.PlanningQuality))
		gomega.Expect(slices.ContainsFunc(audit.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "planning_quality_feedback_reported" && finding.Severity == "high"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected planning-quality finding, got %#v", audit.Findings))

	})
	ginkgo.It("harness feedback rejects unstructured reports", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "harness_feedback",
			Category:  "tool_gap",
			Component: "context_pack",
			Observed:  "missing expected/impact",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected harness_feedback to reject incomplete reports, got %#v", result))

	})
	ginkgo.It("file stats counts file and directory scopes", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "a.go"), "package main\n\nfunc main() {}\n")
		writeWorkspaceFile(filepath.Join(root, "nested", "b.go"), "package nested\n")
		writeWorkspaceFile(filepath.Join(root, "nested", "note.txt"), "hello\nworld\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "file_stats", Path: "a.go"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("file_stats file returned error: %v", result.Error))

		var fileReport fileStatsReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &fileReport)).To(gomega.Succeed())
		gomega.Expect(fileReport.Files).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected file stats: %#v", fileReport))
		gomega.Expect(fileReport.Lines).To(gomega.BeNumerically("==", 3), fmt.Sprintf("unexpected file stats: %#v", fileReport))
		gomega.Expect(fileReport.Bytes).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("unexpected file stats: %#v", fileReport))

		result = mgr.Execute(context.Background(), ToolRequest{Tool: "file_stats", Path: ".", Recursive: true, Glob: "*.go", MaxDepth: 2})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("file_stats directory returned error: %v", result.Error))

		var dirReport fileStatsReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &dirReport)).To(gomega.Succeed())
		gomega.Expect(dirReport.Files).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected directory stats: %#v", dirReport))
		gomega.Expect(dirReport.Lines).To(gomega.BeNumerically("==", 4), fmt.Sprintf("unexpected directory stats: %#v", dirReport))

	})
	ginkgo.It("harness audit behavior default includes telemetry logs", func() {
		root := ginkgo.GinkgoT().TempDir()
		for _, path := range []string{
			filepath.Join(root, "logs", "conversations", "session.JSONL"),
			filepath.Join(root, "logs", "telemetry", "tool.JSONL"),
		} {
			lines := []string{
				`{"type":"tool_request","tool":"pwd","action":"pwd"}`,
				`{"type":"tool_result","tool":"pwd","summary":"Printed current working directory","error":""}`,
			}
			if strings.Contains(path, "telemetry") {
				lines = append(lines,
					`{"type":"tool_request","tool":"context_status","action":"context_status","discovery_class":"context_status"}`,
					`{"type":"tool_result","tool":"context_status","summary":"Context index is fresh","error":"","context_store_kind":"sqlite-graph-v1","context_active_backend":"sqlite-graph-v1","context_active_backend_status":"active","context_storage_backends":["json-graph-v1:export","sqlite-graph-v1:active"],"context_indexed":true,"context_fresh":true,"context_files_indexed":2,"context_symbols_indexed":5,"context_edges_indexed":8}`,
					`{"type":"tool_request","tool":"harness_feedback","action":"harness_feedback","discovery_class":"harness_feedback"}`,
					`{"type":"tool_result","tool":"harness_feedback","summary":"Recorded tool_gap feedback for context_pack","error":"","feedback_category":"tool_gap","feedback_component":"context_pack","feedback_confidence":"verified","feedback_severity":"high","feedback_evidence_count":2}`,
				)
			}

			lines = append(lines, "")
			writeWorkspaceFile(path, strings.Join(lines,

				"\n"),
			)
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.ContractCoverage).To(gomega.BeNil(), fmt.Sprintf("expected no contract coverage without autonomy events, got %#v", report.ContractCoverage))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(`"contract_coverage"`), fmt.Sprintf("expected no serialized contract_coverage without autonomy events, got %s", result.Output))
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected conversation and telemetry logs to be scanned, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.LogsScanned).To(gomega.BeNumerically("==", 2), fmt.Sprintf("expected conversation and telemetry logs to be scanned, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.Aggregate.Context).NotTo(gomega.BeNil(), fmt.Sprintf("expected audit analyzer to ingest context telemetry fields, got %#v", report.LogBehavior.Aggregate.Context))
		gomega.Expect(report.LogBehavior.Aggregate.Context.StoreKindCounts["sqlite-graph-v1"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected audit analyzer to ingest context telemetry fields, got %#v", report.LogBehavior.Aggregate.Context))
		gomega.Expect(report.LogBehavior.Aggregate.Feedback).NotTo(gomega.BeNil(), fmt.Sprintf("expected audit analyzer to ingest harness feedback telemetry fields, got %#v", report.LogBehavior.Aggregate.Feedback))
		gomega.Expect(report.LogBehavior.Aggregate.Feedback.Total).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected audit analyzer to ingest harness feedback telemetry fields, got %#v", report.LogBehavior.Aggregate.Feedback))
		gomega.Expect(report.LogBehavior.Aggregate.Feedback.ByCategory["tool_gap"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected audit analyzer to ingest harness feedback telemetry fields, got %#v", report.LogBehavior.Aggregate.Feedback))
		gomega.Expect(report.LogBehavior.Aggregate.Feedback.HighSeverity).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected audit analyzer to ingest harness feedback telemetry fields, got %#v", report.LogBehavior.Aggregate.Feedback))
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "agent_harness_feedback_reported" && finding.Severity == "high"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected feedback finding, got %#v", report.Findings))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.Sources, func(source harnessAuditLogSource) bool {
			return source.Pattern == "logs/telemetry/*.jsonl" && source.Matched == 1 && source.Selected == 1
		})).To(gomega.BeTrue(), fmt.Sprintf("expected audit source coverage for telemetry logs, got %#v", report.LogBehavior.Sources))

	})
	ginkgo.It("harness audit behavior includes contract coverage", func() {
		root := ginkgo.GinkgoT().TempDir()

		contract, err := autonomy.NewContract(autonomy.ContractInput{
			Scope: autonomy.ContractScope{
				InvocationKind:    autonomy.InvocationSidecarCall,
				TaskFamily:        "sidecar_discovery",
				RuntimeContract:   "grounded_evidence_summary",
				PermissionProfile: "read_only_workspace",
				Sidecar: &autonomy.SidecarContext{
					Transport: "cli",
					Caller:    "codex",
					Profile:   "discovery",
					RequestID: "req-contract-coverage",
					Tool:      "context_pack",
				},
				ToolCall: &autonomy.ToolCallContext{
					ToolCallID: "tool-contract-coverage",
					ToolName:   "context_pack",
					Capability: autonomy.CapabilitySemanticDiscovery,
					SideEffect: autonomy.SideEffectReadOnly,
				},
			},
			Obligations: []autonomy.Obligation{
				{ObligationID: "obl-evidence", Kind: autonomy.ObligationEvidence, Status: autonomy.ObligationOpen, Owner: autonomy.OwnerSidecar, RequiredBy: "sidecar_request"},
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

		occurredAt := time.Date(2026, 5, 13, 21, 0, 0, 0, time.UTC)
		events := []autonomy.Event{
			mustWorkspaceAutonomyEvent(autonomy.NewContractCompiledEvent(contract,
				occurredAt,
				1),
			),
			mustWorkspaceAutonomyEvent(autonomy.NewPolicyEvent(autonomy.EventToolPolicyDecision,

				contract,
				autonomy.ContractPolicyEvent{
					ToolCallID: "tool-contract-coverage",

					ToolName: "context_pack", Capability: autonomy.CapabilitySemanticDiscovery,
					SideEffect: autonomy.SideEffectReadOnly,
					Decision:   autonomy.PolicyObserve,
				}, occurredAt.Add(time.Second),
				2),
			),
			mustWorkspaceAutonomyEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationOpened,

				contract,
				autonomy.Obligation{ObligationID: "obl-evidence",
					Kind: autonomy.ObligationEvidence, Status: autonomy.ObligationOpen, Owner: autonomy.OwnerSidecar, RequiredBy: "sidecar_request",
				}, occurredAt.Add(2*time.Second), 3),
			),
			mustWorkspaceAutonomyEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved,

				contract,
				autonomy.Obligation{
					ObligationID: "obl-evidence",
					Kind:         autonomy.ObligationEvidence, Status: autonomy.ObligationResolved, Owner: autonomy.OwnerSidecar, RequiredBy: "sidecar_request",
					EvidenceRef: "event-context-pack-result",
				}, occurredAt.Add(3*time.Second), 4),
			),
		}

		var lines []string

		for _, event := range events {
			encoded, err := json.Marshal(event)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal event: %v", err))

			lines = append(lines, string(encoded))
		}

		gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract,
			autonomy.ContractGateEvent{
				Gate:                autonomy.GateReadOnly,
				Decision:            autonomy.PolicyObserve,
				PackID:              autonomy.ContractPackCompletionReadiness,
				EnforcementMode:     autonomy.EnforcementModeObserveOnly,
				InvariantID:         "sidecar.read_only_profile",
				ReasonCode:          "read_only_contract",
				ToolCallID:          "tool-contract-coverage",
				ToolName:            "context_pack",
				ObligationRefs:      []string{"obl-evidence"},
				ResolvedObligations: []string{"obl-evidence"},
				ObservedFacts:       []autonomy.ContractObservedFact{"resolved_obligation=obl-evidence"},
				Source:              "harness_audit_test",
			}, occurredAt.Add(4*time.Second), 5),
		)

		encodedGateEvent, err := json.Marshal(gateEvent)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal gate event: %v", err))

		wrappedGateLine, err := json.Marshal(map[string]string{
			"type":    "message",
			"role":    "autonomy_event",
			"content": string(encodedGateEvent),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal wrapped gate event: %v", err))

		lines = append(lines, string(wrappedGateLine))

		frictionEvent := mustWorkspaceAutonomyEvent(autonomy.NewGuardFrictionEvent(contract,
			autonomy.ContractGuardFrictionEvent{Step: 5, Lane: "sidecar",
				Phase:   "discovery",
				PhaseID: "sidecar.discovery",

				ProtocolHash:       "protocol-test",
				InvariantID:        "completion.readiness",
				Subject:            autonomy.GuardFrictionSubjectFinalAnswer,
				Decision:           autonomy.GuardFrictionDecisionReject,
				GuardSurface:       autonomy.GuardFrictionSurfaceCompletionGate,
				FirstInvalidAction: "final_answer", VisibleActionClasses: []string{"final_answer", "blocker"}, VisibleTools: []string{"context_pack"}, SafeAlternatives: []string{"<<QUESTION>>:"}, ModelContractVisible: true,
				RetryOutcome: autonomy.GuardFrictionRetryNotApplicable,
				Avoidability: autonomy.GuardFrictionAvoidabilityUnknown,
				EvidenceRefs: []string{"event-contract-coverage"}}, occurredAt.Add(5*time.Second), 6),
		)

		encodedFrictionEvent, err := json.Marshal(frictionEvent)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal friction event: %v", err))

		wrappedFrictionLine, err := json.Marshal(map[string]string{
			"type":    "message",
			"role":    "autonomy_event",
			"content": string(encodedFrictionEvent),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal wrapped friction event: %v", err))

		lines = append(lines, string(wrappedFrictionLine))
		lines = append(lines, "")
		writeWorkspaceFile(filepath.Join(root, "logs", "telemetry", "20260513-210100.000-contracts.jsonl"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.ContractCoverage).NotTo(gomega.BeNil(), fmt.Sprintf("expected contract coverage for six events, got %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.TotalEvents).To(gomega.BeNumerically("==", 6), fmt.Sprintf("expected contract coverage for six events, got %#v", report.ContractCoverage))
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected log behavior guard friction rollup, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.GuardFriction).NotTo(gomega.BeNil(), fmt.Sprintf("expected log behavior guard friction rollup, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.GuardFriction.Total).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected log behavior guard friction rollup, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.GuardFriction.UnclassifiedFriction).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected log behavior guard friction rollup, got %#v", report.LogBehavior))
		gomega.Expect(report.ContractCoverage.AllClear).To(gomega.BeTrue(), fmt.Sprintf("unexpected contract coverage: %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.PolicyCoverage.DecisionEvents).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected contract coverage: %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.ObligationCoverage.Resolved).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected contract coverage: %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.GateCoverage.DecisionEvents).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected contract coverage: %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.GateCoverage.KindCounts[string(autonomy.GateReadOnly)]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected contract coverage: %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.GuardFrictionCoverage.Total).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected contract coverage: %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.GuardFrictionCoverage.GuardSurfaceCounts[string(autonomy.GuardFrictionSurfaceCompletionGate)]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected contract coverage: %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.SidecarToolCorrelation.CorrelatedEventCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("unexpected contract coverage: %#v", report.ContractCoverage))
		gomega.Expect(report.ContractCoverage.GateCoverage.OpenObligationRefs).To(gomega.BeEmpty(), fmt.Sprintf("expected resolved gate obligations with no open refs, got %#v", report.ContractCoverage.GateCoverage))
		gomega.Expect(report.ContractCoverage.GateCoverage.InconsistentOpenObligationRefs).To(gomega.BeEmpty(), fmt.Sprintf("expected resolved gate obligations with no open refs, got %#v", report.ContractCoverage.GateCoverage))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("contract_events=6"), fmt.Sprintf("expected summary to include contract event count, got %q", result.Summary))

	})
	ginkgo.It("harness audit ignores legacy turn phase projection contradictions", func() {
		encoded := mustWorkspaceTextualTurnPhaseProjectionEvent(harnessAuditTurnPhaseProjectionIntroducedAt.Add(-time.Minute))

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(encoded), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
		gomega.Expect(report.TurnPhaseCoverage.Total).To(gomega.BeNumerically("==", 0), fmt.Sprintf("legacy turn-phase projection should not feed current coverage, got %#v", report.TurnPhaseCoverage))
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding autonomy.EventAuditFinding) bool {
			return finding.ID == "turn_phase_contract_contradiction"
		})).To(gomega.BeFalse(), fmt.Sprintf("legacy turn-phase projection should not create current contradiction findings, got %#v", report.Findings))

		historicalEvents, historicalWarnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(encoded), harnessAuditAutonomyDecodeOptions{
			PreserveLegacyTurnPhaseProjection: true,
		})
		gomega.Expect(historicalWarnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected historical warnings: %#v", historicalWarnings))

		historicalFindings := historicalTurnPhaseProjectionFindingsFromEvents(historicalEvents)
		gomega.Expect(slices.ContainsFunc(historicalFindings, func(finding harnessAuditFinding) bool {
			return finding.ID == "historical_turn_phase_contract_contradiction" &&
				strings.Contains(finding.Evidence, "pre-current turn-phase projection contradiction(s)")
		})).To(gomega.BeTrue(), fmt.Sprintf("legacy turn-phase contradiction should be retained as historical finding"))

	})
	ginkgo.It("harness audit still flags current turn phase projection contradictions", func() {
		encoded := mustWorkspaceTextualTurnPhaseProjectionEvent(harnessAuditTurnPhaseProjectionIntroducedAt.Add(time.Minute))

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(encoded), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
		gomega.Expect(report.TurnPhaseCoverage.Total).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("current turn-phase projection should feed coverage"))
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding autonomy.EventAuditFinding) bool {
			return finding.ID == "turn_phase_contract_contradiction"
		})).To(gomega.BeTrue(), fmt.Sprintf("current malformed turn-phase projection should still be flagged, got %#v", report.Findings))

	})
	ginkgo.It("harness audit classifies pre fix open evidence final answer projection as historical", func() {
		encoded := mustWorkspaceOpenEvidenceFinalAnswerProjectionEvent(harnessAuditOpenEvidenceFinalAnswerProjectionFixedAt.Add(-time.Minute))
		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "prefixed.jsonl")
		gomega.Expect(os.WriteFile(path, []byte(encoded+"\n"), 0o644)).To(gomega.Succeed())

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(encoded), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(currentHarnessAuditAutonomyEvents(events), autonomy.EventAuditOptions{})
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding autonomy.EventAuditFinding) bool {
			return finding.ID == "turn_phase_contract_contradiction"
		})).To(gomega.BeFalse(), fmt.Sprintf("pre-fix residual-blocker projection should not raise current contradiction, got %#v", report.Findings))

		historical := historicalOpenEvidenceFinalAnswerProjectionFindings(filepath.Dir(path), []string{path})
		gomega.Expect(slices.ContainsFunc(historical, func(finding harnessAuditFinding) bool {
			return finding.ID == "historical_open_evidence_final_answer_projection" &&
				strings.Contains(finding.Evidence, "pre-fix turn-phase projection")
		})).To(gomega.BeTrue(), fmt.Sprintf("expected pre-fix projection to remain historical evidence, got %#v", historical))

	})
	ginkgo.It("harness audit still flags post fix open evidence final answer projection", func() {
		encoded := mustWorkspaceOpenEvidenceFinalAnswerProjectionEvent(harnessAuditOpenEvidenceFinalAnswerProjectionFixedAt.Add(time.Minute))

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(encoded), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(currentHarnessAuditAutonomyEvents(events), autonomy.EventAuditOptions{})
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding autonomy.EventAuditFinding) bool {
			return finding.ID == "turn_phase_contract_contradiction"
		})).To(gomega.BeTrue(), fmt.Sprintf("post-fix residual-blocker projection must remain a current contradiction, got %#v", report.Findings))

	})
})

func mustWorkspaceTextualTurnPhaseProjectionEvent(occurredAt time.Time) string {
	ginkgo.GinkgoHelper()

	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind:    autonomy.InvocationLane,
			Lane:              "explore",
			LaneRunID:         "turn-phase-projection-session",
			TaskFamily:        "debug_logs",
			RuntimeContract:   "grounded_explanation",
			PermissionProfile: "read_only_workspace",
		},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

	phase := autonomy.TurnPhaseContractEvent{
		SchemaVersion: "idiom.turn_phase_contract.v1",
		ContractID:    contract.ContractID,
		PhaseID:       "generic",
		ModelCallID:   contract.ContractID + ":model_call_1",
		ProtocolHash:  "sha256:turn-phase-projection-test",
		Phase:         "generic",
		AllowedTools:  []string{"read_file"},
		ActionPolicy: autonomy.TurnActionClassPolicyEvent{
			AllowedActionClasses:   []string{"final_answer", "read"},
			TerminalResponseShapes: []string{"final answer"},
			CallableToolNames:      []string{"read_file"},
		},
		RuntimeCall: autonomy.RuntimeCallContractEvent{
			ToolTransportMode:        "textual",
			ExpectedVisibleOutput:    "visible_text_tool_request",
			ExpressibleActionClasses: []string{"final_answer"},
		},
		TerminalOutcomes: []string{"final answer"},
		EnforcementMode:  "observe_only",
	}
	event := mustWorkspaceAutonomyEvent(autonomy.NewContractPresentedEvent(contract,
		autonomy.ContractLifecycleEvent{Reason: "turn_phase_contract",

			ProtocolHash: phase.ProtocolHash,

			TurnPhaseContract: &phase, SurfaceSummaries: []autonomy.TurnPhaseSurfaceSummaryEvent{autonomy.SurfaceSummaryFromTurnPhaseContract(phase)}},
		occurredAt, 1),
	)

	return mustMarshalWorkspaceAutonomyEvent(event)
}

func mustWorkspaceOpenEvidenceFinalAnswerProjectionEvent(occurredAt time.Time) string {
	ginkgo.GinkgoHelper()

	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind:    autonomy.InvocationLane,
			Lane:              "explore",
			LaneRunID:         "open-evidence-final-answer-session",
			TaskFamily:        "conversation",
			RuntimeContract:   "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

	phase := autonomy.TurnPhaseContractEvent{
		SchemaVersion: "idiom.turn_phase_contract.v1",
		ContractID:    contract.ContractID,
		PhaseID:       "generic",
		ModelCallID:   contract.ContractID + ":model_call_1",
		ProtocolHash:  "sha256:open-evidence-final-answer-test",
		Phase:         "read_only_evidence_pending",
		ActionPolicy: autonomy.TurnActionClassPolicyEvent{
			AllowedActionClasses:   []string{"final_answer", "read", "blocker"},
			TerminalResponseShapes: []string{"final answer", "<<QUESTION>>:"},
			CallableToolNames:      []string{"read_file"},
		},
		RuntimeCall: autonomy.RuntimeCallContractEvent{
			ToolTransportMode:        "native",
			ExpectedVisibleOutput:    "native_tool_call",
			ExpressibleActionClasses: []string{"final_answer", "read", "blocker"},
		},
		TerminalOutcomes: []string{"final answer", "<<QUESTION>>:"},
		EvidenceState: autonomy.PhaseEvidenceStateEvent{
			ResidualBlockers: []string{"context_pack blocked by context freshness (missing_index)"},
		},
		EnforcementMode: "observe_only",
	}
	event := mustWorkspaceAutonomyEvent(autonomy.NewContractPresentedEvent(contract,
		autonomy.ContractLifecycleEvent{Reason: "turn_phase_contract",

			ProtocolHash: phase.ProtocolHash,

			TurnPhaseContract: &phase, SurfaceSummaries: []autonomy.TurnPhaseSurfaceSummaryEvent{autonomy.SurfaceSummaryFromTurnPhaseContract(phase)}},
		occurredAt, 1),
	)

	return mustMarshalWorkspaceAutonomyEvent(event)
}

var _ = ginkgo.Describe("discovery tools", func() {
	ginkgo.It("harness audit backfills legacy gate pack and enforcement mode", func() {
		contract, err := autonomy.NewContract(autonomy.ContractInput{
			Scope: autonomy.ContractScope{
				InvocationKind: autonomy.InvocationLane,
				Lane:           "implement",
				LaneRunID:      "legacy-gate-session",
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

		gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract,
			autonomy.ContractGateEvent{Gate: autonomy.GateReadOnly, Decision: autonomy.PolicyObserve,

				InvariantID: "read_only.readiness", ReasonCode: "read_only_contract", Source: "legacy_harness_audit_test"}, harnessAuditGateMetadataIntroducedAt.Add(-time.Minute), 1),
		)
		encoded := mustMarshalWorkspaceAutonomyEvent(gateEvent)
		legacy := mutateWorkspaceAutonomyGateFields(encoded, func(gate map[string]any) {
			delete(gate, "pack_id")
			delete(gate, "enforcement_mode")
		})

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(legacy), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
		gomega.Expect(report.GateCoverage.MissingPackIDRefs).To(gomega.BeEmpty(), fmt.Sprintf("legacy absent fields should be backfilled, got %#v", report.GateCoverage))
		gomega.Expect(report.GateCoverage.MissingEnforcementModeRefs).To(gomega.BeEmpty(), fmt.Sprintf("legacy absent fields should be backfilled, got %#v", report.GateCoverage))
		gomega.Expect(report.GateCoverage.DecisionCountsByPackID[string(autonomy.ContractPackCompletionReadiness)]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected backfilled pack/mode counts, got %#v", report.GateCoverage))
		gomega.Expect(report.GateCoverage.DecisionCountsByEnforcementMode[string(autonomy.EnforcementModeObserveOnly)]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected backfilled pack/mode counts, got %#v", report.GateCoverage))

	})
	ginkgo.It("harness audit still flags current absent gate pack and enforcement mode", func() {
		contract, err := autonomy.NewContract(autonomy.ContractInput{
			Scope: autonomy.ContractScope{
				InvocationKind: autonomy.InvocationLane,
				Lane:           "implement",
				LaneRunID:      "current-absent-gate-session",
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

		gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract,
			autonomy.ContractGateEvent{Gate: autonomy.GateReadOnly, Decision: autonomy.PolicyObserve,

				InvariantID: "read_only.readiness", ReasonCode: "read_only_contract", Source: "current_harness_audit_test"}, harnessAuditGateMetadataIntroducedAt.Add(time.Minute), 1),
		)
		encoded := mustMarshalWorkspaceAutonomyEvent(gateEvent)
		currentAbsent := mutateWorkspaceAutonomyGateFields(encoded, func(gate map[string]any) {
			delete(gate, "pack_id")
			delete(gate, "enforcement_mode")
		})

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(currentAbsent), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
		gomega.Expect(report.GateCoverage.MissingPackIDRefs).To(gomega.HaveLen(1), fmt.Sprintf("current absent fields should still be flagged, got %#v", report.GateCoverage))
		gomega.Expect(report.GateCoverage.MissingEnforcementModeRefs).To(gomega.HaveLen(1), fmt.Sprintf("current absent fields should still be flagged, got %#v", report.GateCoverage))

	})
	ginkgo.It("harness audit still flags explicit empty gate pack and enforcement mode", func() {
		contract, err := autonomy.NewContract(autonomy.ContractInput{
			Scope: autonomy.ContractScope{
				InvocationKind: autonomy.InvocationLane,
				Lane:           "implement",
				LaneRunID:      "explicit-empty-gate-session",
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

		gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract,
			autonomy.ContractGateEvent{Gate: autonomy.GateReadOnly, Decision: autonomy.PolicyObserve,

				InvariantID: "read_only.readiness", ReasonCode: "read_only_contract", Source: "legacy_harness_audit_test"}, harnessAuditGateMetadataIntroducedAt.Add(-time.Minute), 1),
		)
		encoded := mustMarshalWorkspaceAutonomyEvent(gateEvent)
		explicitEmpty := mutateWorkspaceAutonomyGateFields(encoded, func(gate map[string]any) {
			gate["pack_id"] = ""
			gate["enforcement_mode"] = ""
		})

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(explicitEmpty), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
		gomega.Expect(report.GateCoverage.MissingPackIDRefs).To(gomega.HaveLen(1), fmt.Sprintf("explicit empty fields should still be flagged, got %#v", report.GateCoverage))
		gomega.Expect(report.GateCoverage.MissingEnforcementModeRefs).To(gomega.HaveLen(1), fmt.Sprintf("explicit empty fields should still be flagged, got %#v", report.GateCoverage))

	})
	ginkgo.It("harness audit backfills legacy blocking gate safe alternatives", func() {
		contract, err := autonomy.NewContract(autonomy.ContractInput{
			Scope: autonomy.ContractScope{
				InvocationKind: autonomy.InvocationLane,
				Lane:           "implement",
				LaneRunID:      "legacy-blocking-gate-session",
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

		gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract,
			autonomy.ContractGateEvent{Gate: autonomy.GateCompletion,
				Decision: autonomy.PolicyReject,

				InvariantID: "completion.readiness", ReasonCode: "required_obligations_open",
				Source: "legacy_harness_audit_test",
			}, harnessAuditGateMetadataIntroducedAt.Add(-time.Minute), 1),
		)
		encoded := mustMarshalWorkspaceAutonomyEvent(gateEvent)
		legacy := mutateWorkspaceAutonomyGateFields(encoded, func(gate map[string]any) {
			delete(gate, "pack_id")
			delete(gate, "enforcement_mode")
			delete(gate, "safe_alternatives")
		})

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(legacy), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
		gomega.Expect(report.GateCoverage.MissingPackIDRefs).To(gomega.BeEmpty(), fmt.Sprintf("legacy blocking gate fields should be backfilled, got %#v", report.GateCoverage))
		gomega.Expect(report.GateCoverage.MissingEnforcementModeRefs).To(gomega.BeEmpty(), fmt.Sprintf("legacy blocking gate fields should be backfilled, got %#v", report.GateCoverage))
		gomega.Expect(report.GateCoverage.MissingSafeAlternativesRefs).To(gomega.BeEmpty(), fmt.Sprintf("legacy blocking gate fields should be backfilled, got %#v", report.GateCoverage))
		gomega.Expect(report.GateCoverage.EventsWithSafeAlternatives).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected backfilled safe alternatives count, got %#v", report.GateCoverage))

	})
	ginkgo.It("harness audit still flags current absent blocking gate safe alternatives", func() {
		contract, err := autonomy.NewContract(autonomy.ContractInput{
			Scope: autonomy.ContractScope{
				InvocationKind: autonomy.InvocationLane,
				Lane:           "implement",
				LaneRunID:      "current-absent-safe-alternatives-session",
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

		gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract,
			autonomy.ContractGateEvent{Gate: autonomy.GateCompletion,
				Decision: autonomy.PolicyReject,

				PackID:          autonomy.ContractPackCompletionReadiness,
				EnforcementMode: autonomy.EnforcementModeEnforce, InvariantID: "completion.readiness", ReasonCode: "required_obligations_open",
				Source: "current_harness_audit_test"},
			harnessAuditGateMetadataIntroducedAt.Add(time.Minute), 1),
		)
		encoded := mustMarshalWorkspaceAutonomyEvent(gateEvent)
		currentAbsent := mutateWorkspaceAutonomyGateFields(encoded, func(gate map[string]any) {
			delete(gate, "safe_alternatives")
		})

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(currentAbsent), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
		gomega.Expect(report.GateCoverage.MissingSafeAlternativesRefs).To(gomega.HaveLen(1), fmt.Sprintf("current absent safe alternatives should still be flagged, got %#v", report.GateCoverage))

	})
	ginkgo.It("harness audit still flags explicit empty blocking gate safe alternatives", func() {
		contract, err := autonomy.NewContract(autonomy.ContractInput{
			Scope: autonomy.ContractScope{
				InvocationKind: autonomy.InvocationLane,
				Lane:           "implement",
				LaneRunID:      "explicit-empty-safe-alternatives-session",
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

		gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract,
			autonomy.ContractGateEvent{Gate: autonomy.GateCompletion,
				Decision: autonomy.PolicyReject,

				PackID:          autonomy.ContractPackCompletionReadiness,
				EnforcementMode: autonomy.EnforcementModeEnforce, InvariantID: "completion.readiness", ReasonCode: "required_obligations_open",
				Source: "legacy_harness_audit_test"}, harnessAuditGateMetadataIntroducedAt.Add(-time.Minute), 1),
		)
		encoded := mustMarshalWorkspaceAutonomyEvent(gateEvent)
		explicitEmpty := mutateWorkspaceAutonomyGateFields(encoded, func(gate map[string]any) {
			gate["safe_alternatives"] = []any{}
		})

		events, warnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(explicitEmpty), harnessAuditAutonomyDecodeOptions{})
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))

		report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
		gomega.Expect(report.GateCoverage.MissingSafeAlternativesRefs).To(gomega.HaveLen(1), fmt.Sprintf("explicit empty safe alternatives should still be flagged, got %#v", report.GateCoverage))

	})
	ginkgo.It("harness audit contract coverage sorts events before auditing", func() {
		root := ginkgo.GinkgoT().TempDir()

		contract, err := autonomy.NewContract(autonomy.ContractInput{
			Scope: autonomy.ContractScope{
				InvocationKind:  autonomy.InvocationLane,
				Lane:            "implement",
				RuntimeContract: "implementation_result",
			},
			Obligations: []autonomy.Obligation{
				{ObligationID: "obl-write-file", Kind: autonomy.ObligationWrite, Status: autonomy.ObligationOpen, Owner: autonomy.OwnerLane, RequiredBy: "required_edit_paths"},
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewContract returned error: %v", err))

		occurredAt := time.Date(2026, 5, 13, 21, 0, 0, 0, time.UTC)
		compiled := mustWorkspaceAutonomyEvent(autonomy.NewContractCompiledEvent(contract,
			occurredAt,
			1),
		)
		opened := mustWorkspaceAutonomyEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationOpened,

			contract,
			autonomy.Obligation{ObligationID: "obl-write-file",
				Kind: autonomy.ObligationWrite, Status: autonomy.ObligationOpen, Owner: autonomy.OwnerLane, RequiredBy: "required_edit_paths",
			}, occurredAt.Add(time.Second),
			2),
		)
		resolved := mustWorkspaceAutonomyEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved,

			contract,
			autonomy.Obligation{
				ObligationID: "obl-write-file",

				Kind: autonomy.ObligationWrite, Status: autonomy.ObligationResolved, Owner: autonomy.OwnerLane, RequiredBy: "required_edit_paths",
				EvidenceRef: "event-write",
			}, occurredAt.Add(2*time.Second), 3),
		)
		openPath := filepath.Join(root, "logs", "telemetry", "open.jsonl")
		resolvePath := filepath.Join(root, "logs", "telemetry", "resolve.jsonl")
		writeWorkspaceFile(openPath, strings.Join([]string{
			mustMarshalWorkspaceAutonomyEvent(compiled),
			mustMarshalWorkspaceAutonomyEvent(opened),
			"",
		}, "\n"))
		writeWorkspaceFile(resolvePath, strings.Join([]string{
			mustMarshalWorkspaceAutonomyEvent(resolved),
			mustMarshalWorkspaceAutonomyEvent(resolved),
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		report, warnings := mgr.auditContractEventCoverageWithFilter([]string{resolvePath, openPath}, nil)
		gomega.Expect(warnings).To(gomega.BeEmpty(), fmt.Sprintf("unexpected warnings: %#v", warnings))
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected contract coverage report")
		gomega.Expect(report.ObligationCoverage.UnmatchedObligations).To(gomega.BeEmpty(), fmt.Sprintf("out-of-order and duplicate autonomy events should not create unmatched obligations: %#v", report.ObligationCoverage.UnmatchedObligations))
		gomega.Expect(report.TotalEvents).To(gomega.BeNumerically("==", 3), fmt.Sprintf("expected sorted/deduped event coverage, got %#v", report))
		gomega.Expect(report.ObligationCoverage.Resolved).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected sorted/deduped event coverage, got %#v", report))

	})
	ginkgo.It("harness audit behavior rejects invalid autonomy events", func() {
		root := ginkgo.GinkgoT().TempDir()
		lines := []string{
			`{"schema_version":"idiom.autonomy.event.v1","event_id":"event-invalid","type":"not_a_real_event","effect":"observe_only","occurred_at":"2026-05-13T21:01:00Z","sequence":1,"contract_id":"contract_invalid","lifecycle":{"reason":"bad type"}}`,
			"",
		}
		writeWorkspaceFile(filepath.Join(root, "logs", "telemetry", "20260513-210100.000-contracts.jsonl"), strings.Join(lines, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.ContractCoverage).To(gomega.BeNil(), fmt.Sprintf("invalid autonomy event should not enter contract coverage, got %#v", report.ContractCoverage))
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid autonomy event warning, got %#v", report.LogBehavior))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.Warnings, func(warning string) bool {
			return strings.Contains(warning, "invalid autonomy event")
		})).To(gomega.BeTrue(), fmt.Sprintf("expected invalid autonomy event warning, got %#v", report.LogBehavior))
		gomega.Expect(slices.ContainsFunc(report.HistoricalFindings, func(finding harnessAuditFinding) bool {
			return finding.ID == "historical_contract_event_invalid" && finding.Severity == "info"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected historical invalid contract event finding, got current=%#v historical=%#v", report.Findings, report.HistoricalFindings))

	})
})

func mustWorkspaceAutonomyEvent(event autonomy.Event, err error) autonomy.Event {
	if err != nil {
		panic(err)
	}

	return event
}

func mustMarshalWorkspaceAutonomyEvent(event autonomy.Event) string {
	ginkgo.GinkgoHelper()

	encoded, err := json.Marshal(event)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal event: %v", err))

	return string(encoded)
}

func mutateWorkspaceAutonomyGateFields(encoded string, mutate func(map[string]any)) string {
	ginkgo.GinkgoHelper()

	var record map[string]any
	gomega.Expect(json.Unmarshal([]byte(encoded), &record)).To(gomega.Succeed())

	gate, ok := record["gate"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("event has no gate object: %s", encoded))

	mutate(gate)

	out, err := json.Marshal(record)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal mutated event: %v", err))

	return string(out)
}

var _ = ginkgo.Describe("discovery tools", func() {
	ginkgo.It("harness audit behavior default includes idiom conversation logs", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, ".idiom", "logs", "conversations", "session.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"message","role":"user","content":"write the brief"}`,
			`{"type":"tool_result","tool":"completion_gate","step":3,"summary":"Mutation completion gate rejected final answer","error":"this task asks for a workspace change, but no successful write tool result is present"}`,
			`{"type":"tool_request","tool":"read_file","action":"read_file:native/src/example.cpp","path":"native/src/example.cpp"}`,
			`{"type":"tool_result","tool":"read_file","summary":"Read native/src/example.cpp","error":""}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected idiom conversation log to be scanned, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.LogsScanned).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected idiom conversation log to be scanned, got %#v", report.LogBehavior))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.Sources, func(source harnessAuditLogSource) bool {
			return source.Pattern == ".idiom/logs/conversations/*.jsonl" && source.Matched == 1 && source.Selected == 1
		})).To(gomega.BeTrue(), fmt.Sprintf("expected audit source coverage for .idiom conversation logs, got %#v", report.LogBehavior.Sources))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.TopPatterns, func(hit eval.PerfPatternHit) bool {
			return hit.Name == "completion_gate_reopened_discovery" && hit.TotalCount == 1
		})).To(gomega.BeTrue(), fmt.Sprintf("expected completion_gate_reopened_discovery pattern, got %#v", report.LogBehavior.TopPatterns))

		questions, err := json.Marshal(report.ImprovementQuestions)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal improvement questions: %v", err))
		gomega.Expect(string(questions)).To(gomega.ContainSubstring("recovery contract outcomes"), fmt.Sprintf("expected behavior audit to describe recovery contract outcomes, got %s", questions))
		gomega.Expect(string(questions)).NotTo(gomega.ContainSubstring("write the required artifact"), fmt.Sprintf("behavior audit should avoid strategy-prescriptive completion recovery text, got %s", questions))
		gomega.Expect(string(questions)).NotTo(gomega.ContainSubstring("next-action state"), fmt.Sprintf("behavior audit should avoid strategy-prescriptive completion recovery text, got %s", questions))

	})
	ginkgo.It("harness audit default limit is per log source", func() {
		root := ginkgo.GinkgoT().TempDir()
		conversationPath := filepath.Join(root, "logs", "conversations", "session.jsonl")
		writeWorkspaceFile(conversationPath, strings.Join([]string{
			`{"type":"message","role":"user","content":"find missing code"}`,
			`{"type":"tool_request","tool":"search_files","action":"search_files:_:Nope:_"}`,
			`{"type":"tool_result","tool":"search_files","summary":"No file matches for \"Nope\"","error":""}`,
			"",
		}, "\n"))

		baseTime := time.Now().Add(-time.Hour)
		gomega.Expect(os.Chtimes(conversationPath, baseTime, baseTime)).To(gomega.Succeed())

		for i := 0; i < 3; i++ {
			telemetryPath := filepath.Join(root, "logs", "telemetry", fmt.Sprintf("tool-%d.jsonl", i))
			writeWorkspaceFile(telemetryPath, strings.Join([]string{`{"type":"session_start","command":"tool:pwd"}`,

				`{"type":"tool_request","tool":"pwd","action":"pwd"}`,

				`{"type":"tool_result","tool":"pwd","summary":"Printed current working directory","error":""}`,

				""}, "\n"),
			)

			telemetryTime := baseTime.Add(time.Duration(i+1) * time.Minute)
			gomega.Expect(os.Chtimes(telemetryPath, telemetryTime, telemetryTime)).To(gomega.Succeed())

		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 2})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected one conversation plus two telemetry logs, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.LogsScanned).To(gomega.BeNumerically("==", 3), fmt.Sprintf("expected one conversation plus two telemetry logs, got %#v", report.LogBehavior))
		gomega.Expect(slices.ContainsFunc(report.LogBehavior.TopPatterns, func(hit eval.PerfPatternHit) bool {
			return hit.Name == "zero_hit_search" && hit.TotalCount == 1
		})).To(gomega.BeTrue(), fmt.Sprintf("expected default audit to retain conversation behavior findings, got %#v", report.LogBehavior.TopPatterns))
		gomega.Expect(slices.Contains(report.LogBehavior.Warnings, "log scan capped at 2 newest file(s) out of 3 matched for logs/telemetry/*.jsonl")).To(gomega.BeTrue(), fmt.Sprintf("expected per-source telemetry cap warning, got %#v", report.LogBehavior.Warnings))

	})
	ginkgo.It("harness audit allows large explicit log limit", func() {
		if got := harnessAuditLogLimit(3000); got != 3000 {
			ginkgo.Fail(fmt.Sprintf("expected explicit all-log audit limit to be honored, got %d", got))
		}

		if got := harnessAuditLogLimit(harnessAuditMaxLogLimit + 1); got != harnessAuditMaxLogLimit {
			ginkgo.Fail(fmt.Sprintf("expected extreme limit to cap at %d, got %d", harnessAuditMaxLogLimit, got))
		}
	})
	ginkgo.It("harness audit flags incomplete implement lifecycle", func() {
		root := ginkgo.GinkgoT().TempDir()
		conversationPath := filepath.Join(root, ".idiom", "logs", "conversations", "session.jsonl")
		telemetryPath := filepath.Join(root, "logs", "telemetry", "implement.jsonl")
		writeWorkspaceFile(conversationPath, strings.Join([]string{
			`{"type":"session_start","workspace_root":"` + root + `"}`,
			`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
			"",
		}, "\n"))
		writeWorkspaceFile(telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
			fmt.Sprintf(`{"type":"process_start","command":"implement","pid":%d,"workspace_root":"%s"}`, harnessAuditTestStoppedPID(), root),
			`{"type":"model_session","session_id":"session","debug_log_path":"` + conversationPath + `"}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected one incomplete session, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.IncompleteSessions).To(gomega.HaveLen(1), fmt.Sprintf("expected one incomplete session, got %#v", report.LogBehavior))

		session := report.LogBehavior.IncompleteSessions[0]
		gomega.Expect(session.TelemetryLog).To(gomega.Equal("logs/telemetry/implement.jsonl"), fmt.Sprintf("unexpected incomplete lifecycle session: %#v", session))
		gomega.Expect(session.ConversationLog).To(gomega.Equal(".idiom/logs/conversations/session.jsonl"), fmt.Sprintf("unexpected incomplete lifecycle session: %#v", session))
		gomega.Expect(session.Missing).To(gomega.ContainElement("process_exit"), fmt.Sprintf("unexpected incomplete lifecycle session: %#v", session))
		gomega.Expect(session.Missing).To(gomega.ContainElement("session_summary"), fmt.Sprintf("unexpected incomplete lifecycle session: %#v", session))
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "incomplete_implement_session_lifecycle" && finding.Severity == "high"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected incomplete lifecycle finding, got %#v", report.Findings))

	})
	ginkgo.It("harness audit accepts model closeout telemetry as session summary evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		conversationPath := filepath.Join(root, ".idiom", "logs", "conversations", "session.jsonl")
		telemetryPath := filepath.Join(root, "logs", "telemetry", "implement.jsonl")
		writeWorkspaceFile(conversationPath, strings.Join([]string{
			`{"type":"session_start","workspace_root":"` + root + `"}`,
			`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
			"",
		}, "\n"))
		writeWorkspaceFile(telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
			fmt.Sprintf(`{"type":"process_start","command":"implement","pid":%d,"workspace_root":"%s"}`, harnessAuditTestStoppedPID(), root),
			`{"type":"model_session","session_id":"session","debug_log_path":"` + conversationPath + `"}`,
			`{"type":"model_session_closeout","session_id":"session","debug_log_path":"` + conversationPath + `","summary_event":"session_summary","model_closed":true}`,
			`{"type":"process_exit","command":"implement","exit_code":0}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		if report.LogBehavior != nil {
			gomega.Expect(report.LogBehavior.IncompleteSessions).To(gomega.BeEmpty(), fmt.Sprintf("closeout telemetry should satisfy session-summary lifecycle evidence, got %#v", report.LogBehavior.IncompleteSessions))
		}
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "incomplete_implement_session_lifecycle"
		})).To(gomega.BeFalse(), fmt.Sprintf("expected no incomplete lifecycle finding with model closeout telemetry, got %#v", report.Findings))

	})
})

func harnessAuditTestStoppedPID() int {
	ginkgo.GinkgoHelper()

	for pid := 999999; pid > 900000; pid-- {
		if !processIDAppearsRunning(pid) {
			return pid
		}
	}
	ginkgo.Fail("could not find a non-running PID for harness audit fixture")

	return 0
}

var _ = ginkgo.Describe("discovery tools", func() {
	ginkgo.It("harness audit classifies active implement lifecycle as in progress", func() {
		root := ginkgo.GinkgoT().TempDir()
		conversationPath := filepath.Join(root, ".idiom", "logs", "conversations", "active.jsonl")
		telemetryPath := filepath.Join(root, "logs", "telemetry", "active-implement.jsonl")
		writeWorkspaceFile(conversationPath, strings.Join([]string{
			`{"type":"session_start","workspace_root":"` + root + `"}`,
			`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
			"",
		}, "\n"))
		writeWorkspaceFile(telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
			fmt.Sprintf(`{"type":"process_start","command":"implement","pid":%d,"workspace_root":"%s"}`, os.Getpid(), root),
			`{"type":"model_session","session_id":"active","debug_log_path":"` + conversationPath + `"}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected one in-progress session, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.InProgressSessions).To(gomega.HaveLen(1), fmt.Sprintf("expected one in-progress session, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.IncompleteSessions).To(gomega.BeEmpty(), fmt.Sprintf("active session should not be incomplete, got %#v", report.LogBehavior.IncompleteSessions))
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "incomplete_implement_session_lifecycle"
		})).To(gomega.BeFalse(), fmt.Sprintf("expected no incomplete lifecycle finding for active process, got %#v", report.Findings))

	})
	ginkgo.It("harness audit classifies active tui implementation conversation as in progress", func() {
		root := ginkgo.GinkgoT().TempDir()
		conversationPath := filepath.Join(root, "logs", "conversations", "active-tui.jsonl")
		telemetryPath := filepath.Join(root, "logs", "telemetry", "active-tui.jsonl")
		writeWorkspaceFile(conversationPath, strings.Join([]string{
			`{"type":"session_start","workspace_root":"` + root + `"}`,
			`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
			"",
		}, "\n"))
		writeWorkspaceFile(telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"tui","workspace_root":"` + root + `"}`,
			fmt.Sprintf(`{"type":"process_start","command":"tui","pid":%d,"workspace_root":"%s"}`, os.Getpid(), root),
			`{"type":"model_session","session_id":"active-tui","debug_log_path":"` + conversationPath + `"}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected one in-progress TUI session, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.InProgressSessions).To(gomega.HaveLen(1), fmt.Sprintf("expected one in-progress TUI session, got %#v", report.LogBehavior))

		session := report.LogBehavior.InProgressSessions[0]
		gomega.Expect(session.TelemetryLog).To(gomega.Equal("logs/telemetry/active-tui.jsonl"), fmt.Sprintf("unexpected in-progress TUI session: %#v", session))
		gomega.Expect(session.ConversationLog).To(gomega.Equal("logs/conversations/active-tui.jsonl"), fmt.Sprintf("unexpected in-progress TUI session: %#v", session))
		gomega.Expect(session.Command).To(gomega.Equal("tui"), fmt.Sprintf("unexpected in-progress TUI session: %#v", session))
		gomega.Expect(report.LogBehavior.IncompleteSessions).To(gomega.BeEmpty(), fmt.Sprintf("active TUI conversation should not be incomplete, got %#v", report.LogBehavior.IncompleteSessions))
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "incomplete_implement_session_lifecycle"
		})).To(gomega.BeFalse(), fmt.Sprintf("expected no incomplete lifecycle finding for active TUI process, got %#v", report.Findings))

	})
	ginkgo.It("harness audit links stopped tui implementation conversation to telemetry lifecycle", func() {
		root := ginkgo.GinkgoT().TempDir()
		conversationPath := filepath.Join(root, "logs", "conversations", "20260520-090102.111.jsonl")
		telemetryPath := filepath.Join(root, "logs", "telemetry", "20260520-090101.999-tui-123.jsonl")
		writeWorkspaceFile(conversationPath, strings.Join([]string{
			`{"type":"session_start","workspace_root":"` + root + `"}`,
			`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
			"",
		}, "\n"))
		writeWorkspaceFile(telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"tui","workspace_root":"` + root + `"}`,
			fmt.Sprintf(`{"type":"process_start","command":"tui","pid":%d,"workspace_root":"%s"}`, harnessAuditTestStoppedPID(), root),
			`{"type":"model_session","session_id":"20260520-090102.111","debug_log_path":"` + conversationPath + `"}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected one incomplete TUI-backed implementation session, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.IncompleteSessions).To(gomega.HaveLen(1), fmt.Sprintf("expected one incomplete TUI-backed implementation session, got %#v", report.LogBehavior))

		session := report.LogBehavior.IncompleteSessions[0]
		gomega.Expect(session.TelemetryLog).To(gomega.Equal("logs/telemetry/20260520-090101.999-tui-123.jsonl"), fmt.Sprintf("unexpected stopped TUI lifecycle session: %#v", session))
		gomega.Expect(session.ConversationLog).To(gomega.Equal("logs/conversations/20260520-090102.111.jsonl"), fmt.Sprintf("unexpected stopped TUI lifecycle session: %#v", session))
		gomega.Expect(session.Command).To(gomega.Equal("tui"), fmt.Sprintf("unexpected stopped TUI lifecycle session: %#v", session))
		gomega.Expect(session.Missing).To(gomega.ContainElement("process_exit"), fmt.Sprintf("unexpected stopped TUI lifecycle session: %#v", session))
		gomega.Expect(session.Missing).To(gomega.ContainElement("session_summary"), fmt.Sprintf("unexpected stopped TUI lifecycle session: %#v", session))
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "incomplete_implement_session_lifecycle"
		})).To(gomega.BeFalse(), fmt.Sprintf("pre-closeout TUI lifecycle gaps should not raise current high finding, got %#v", report.Findings))
		gomega.Expect(slices.ContainsFunc(report.HistoricalFindings, func(finding harnessAuditFinding) bool {
			return finding.ID == "historical_incomplete_implement_session_lifecycle" && finding.Severity == "info"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected historical lifecycle finding, got %#v", report.HistoricalFindings))

	})
	ginkgo.It("harness audit timestamp from label parses dated replay logs", func() {
		got, ok := harnessAuditTimestampFromLabel("tool-governance-scripted-contract-replay-20260525")
		gomega.Expect(ok).To(gomega.BeTrue(), "expected dated replay label to parse")

		want := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
		gomega.Expect(got.Equal(want)).To(gomega.BeTrue(), fmt.Sprintf("timestamp = %s, want %s", got, want))

	})
	ginkgo.It("harness audit accepts tui model closeout telemetry as session summary evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		conversationPath := filepath.Join(root, "logs", "conversations", "closed-tui.jsonl")
		telemetryPath := filepath.Join(root, "logs", "telemetry", "closed-tui.jsonl")
		writeWorkspaceFile(conversationPath, strings.Join([]string{
			`{"type":"session_start","workspace_root":"` + root + `"}`,
			`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
			"",
		}, "\n"))
		writeWorkspaceFile(telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"tui","workspace_root":"` + root + `"}`,
			fmt.Sprintf(`{"type":"process_start","command":"tui","pid":%d,"workspace_root":"%s"}`, harnessAuditTestStoppedPID(), root),
			`{"type":"model_session","session_id":"closed-tui","debug_log_path":"` + conversationPath + `"}`,
			`{"type":"model_session_closeout","session_id":"closed-tui","debug_log_path":"` + conversationPath + `","summary_event":"session_summary","model_closed":true}`,
			`{"type":"process_exit","command":"tui","exit_code":0}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		if report.LogBehavior != nil {
			gomega.Expect(report.LogBehavior.IncompleteSessions).To(gomega.BeEmpty(), fmt.Sprintf("TUI closeout telemetry should satisfy session-summary lifecycle evidence, got %#v", report.LogBehavior.IncompleteSessions))
		}
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "incomplete_implement_session_lifecycle"
		})).To(gomega.BeFalse(), fmt.Sprintf("expected no incomplete lifecycle finding with TUI model closeout telemetry, got %#v", report.Findings))

	})
	ginkgo.It("harness audit ignores implement process without model session summary", func() {
		root := ginkgo.GinkgoT().TempDir()
		telemetryPath := filepath.Join(root, "logs", "telemetry", "implement-help.jsonl")
		writeWorkspaceFile(telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
			`{"type":"process_start","command":"implement","pid":123,"workspace_root":"` + root + `"}`,
			`{"type":"process_exit","command":"implement","exit_code":2}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		if report.LogBehavior != nil {
			gomega.Expect(report.LogBehavior.IncompleteSessions).To(gomega.BeEmpty(), fmt.Sprintf("expected no incomplete model session for process-only implement command, got %#v", report.LogBehavior.IncompleteSessions))
		}
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "incomplete_implement_session_lifecycle"
		})).To(gomega.BeFalse(), fmt.Sprintf("expected no incomplete lifecycle finding for process-only implement command, got %#v", report.Findings))

	})
	ginkgo.It("harness audit surfaces local ops degraded telemetry", func() {
		root := ginkgo.GinkgoT().TempDir()
		telemetryPath := filepath.Join(root, "logs", "telemetry", "local-ops.jsonl")
		writeWorkspaceFile(telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
			`{"type":"implement_start","local_ops":{"backend_configured":true,"backend_kind":"mlx","context_budget_state":"degraded","classifier_state":"disabled_fallback"}}`,
			`{"type":"implement_queue","state":"degraded","pool":"qwen-coder","wait_ms":25,"degraded":true}`,
			`{"type":"doctor_readiness_profile","profile":"implement","state":"degraded","degraded":true,"queue_state":"unavailable","context_budget_state":"degraded","route_classifier_state":"degraded","backend_state":"pass"}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected local ops aggregate, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.Aggregate.LocalOps).NotTo(gomega.BeNil(), fmt.Sprintf("expected local ops aggregate, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.Aggregate.LocalOps.QueueDegraded).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected local ops aggregate: %#v", report.LogBehavior.Aggregate.LocalOps))
		gomega.Expect(report.LogBehavior.Aggregate.LocalOps.ContextBudgetDegraded).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("unexpected local ops aggregate: %#v", report.LogBehavior.Aggregate.LocalOps))
		gomega.Expect(slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
			return finding.ID == "local_ops_degraded_mode_reported"
		})).To(gomega.BeTrue(), fmt.Sprintf("expected local ops degraded finding, got %#v", report.Findings))

	})
	ginkgo.It("context telemetry fields include collector reports", func() {
		fields := ContextTelemetryFields("context_index", `{
  "store_kind": "sqlite-graph-v1",
  "collector_version": "old",
  "current_collector_version": "new",
  "collector_version_mismatch": true,
  "stale_file_count": 2,
  "missing_file_count": 1,
  "stale_path_count": 3,
  "confidence": {
    "level": "low",
    "source": "freshness_decision",
    "reason": "budget_exceeded"
  },
  "provenance": {
    "source": "context_graph",
    "operation": "context_pack",
    "store_kind": "sqlite-graph-v1",
    "index_id": "idx123",
    "generated_at": "2026-05-13T09:00:00Z",
    "collector_version": "old",
    "current_collector_version": "new",
    "artifact_path": ".idiom/comprehension/views/abc.json"
  },
  "cache": {
    "state": "hit",
    "source": "graph_views",
    "reason": "fresh_cached_row",
    "cache_key": "abc",
    "pack_id": "abc",
    "workspace_state_fingerprint": "fp123",
    "ranker_weights_fingerprint": "ranker123",
    "runtime_traces_fingerprint": "trace123",
    "freshness_policy": "read_through",
    "read_through_refresh": true,
    "invalidation": {
      "policy": "content_addressed_read_through",
      "state": "valid",
      "stale_result": "cache_miss_recompute",
      "sources": [
        {"source": "index_id", "state": "keyed"},
        {"source": "ranker_weights", "state": "keyed"}
      ]
    }
  },
  "unresolved_gap_details": [
    {"reason": "stale_graph", "state": "stale"}
  ],
  "incremental": true,
  "reused_indexed_files": 5,
  "expanded_scope": "pkg",
  "dependency_refresh_files": ["pkg/a.go", "pkg/b.go"],
  "collector_coverage": ["python_index", "language_environment"],
  "freshness": {
    "policy": "read_through",
    "operation": "context_pack",
    "fresh_for_scope": false,
    "global_fresh": false,
    "scope": "pkg",
    "reason": "budget_exceeded",
    "blocker": "freshness refresh exceeded budget",
    "budget_exceeded": true,
    "checked_path_count": 1,
    "stale_path_count": 1,
    "missing_path_count": 1,
    "checked_paths": ["pkg/a.go"],
    "stale_paths": ["pkg/a.go"],
    "missing_paths": ["pkg/missing.go"],
    "refresh": {
      "attempted": false,
      "scope": "pkg",
      "reason": "budget_exceeded",
      "skipped_reason": "cap exceeded"
    }
  },
  "stale_evidence": {
    "state": "blocked",
    "policy": "read_through",
    "reason": "budget_exceeded",
    "fresh_for_scope": false,
    "global_fresh": false,
    "stale_path_count": 1,
    "missing_path_count": 1
  },
  "stale_symbol_samples": [{"id": "symbol://app.py#run"}],
  "refreshed_stale_symbols": 1,
  "nodes": [
    {"id": "summary://repository", "kind": "summary", "properties": {"model_authored": false}},
    {"id": "summary://package/pkg", "kind": "summary", "properties": {"model_authored": true}}
  ],
  "collector_reports": [
    {"name": "python_lsp", "available": false, "warnings": ["missing"]},
    {"name": "workspace_tool_enrichment", "available": true, "diagnostics": 2, "properties": {"normalized_relationship_edges": 3}}
  ]
}`)
		gomega.Expect(fields["context_collector_reports"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected collector telemetry fields: %#v", fields))
		gomega.Expect(fields["context_collector_unavailable"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected collector telemetry fields: %#v", fields))
		gomega.Expect(fields["context_collector_warnings"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected collector telemetry fields: %#v", fields))
		gomega.Expect(fields["context_enrichment_diagnostics"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected collector telemetry fields: %#v", fields))
		gomega.Expect(fields["context_normalized_relationship_edges"]).To(gomega.BeNumerically("==", 3), fmt.Sprintf("unexpected collector telemetry fields: %#v", fields))
		gomega.Expect(fields["context_collector_coverage"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected collector coverage/stale telemetry fields: %#v", fields))
		gomega.Expect(fields["context_stale_symbol_samples"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected collector coverage/stale telemetry fields: %#v", fields))
		gomega.Expect(fields["context_refreshed_stale_symbols"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected collector coverage/stale telemetry fields: %#v", fields))
		gomega.Expect(fields["context_incremental"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected incremental/summary telemetry fields: %#v", fields))
		gomega.Expect(fields["context_reused_indexed_files"]).To(gomega.BeNumerically("==", 5), fmt.Sprintf("unexpected incremental/summary telemetry fields: %#v", fields))
		gomega.Expect(fields["context_dependency_refresh_files"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected incremental/summary telemetry fields: %#v", fields))
		gomega.Expect(fields["context_summary_nodes"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected incremental/summary telemetry fields: %#v", fields))
		gomega.Expect(fields["context_model_summary_nodes"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected incremental/summary telemetry fields: %#v", fields))
		gomega.Expect(fields["context_expanded_scope"]).To(gomega.Equal("pkg"), fmt.Sprintf("unexpected incremental/summary telemetry fields: %#v", fields))
		gomega.Expect(fields["context_collector_version"]).To(gomega.Equal("old"), fmt.Sprintf("unexpected collector version telemetry fields: %#v", fields))
		gomega.Expect(fields["context_current_collector_version"]).To(gomega.Equal("new"), fmt.Sprintf("unexpected collector version telemetry fields: %#v", fields))
		gomega.Expect(fields["context_collector_version_mismatch"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected collector version telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_policy"]).To(gomega.Equal("read_through"), fmt.Sprintf("unexpected freshness telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_operation"]).To(gomega.Equal("context_pack"), fmt.Sprintf("unexpected freshness telemetry fields: %#v", fields))
		gomega.Expect(fields["context_fresh_for_scope"]).To(gomega.BeFalse(), fmt.Sprintf("unexpected freshness telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_state"]).To(gomega.Equal("blocked"), fmt.Sprintf("unexpected freshness telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_blocked"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected freshness telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_budget_exceeded"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected freshness telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_checked_paths"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected freshness path/refresh telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_stale_paths"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected freshness path/refresh telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_missing_paths"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected freshness path/refresh telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_refresh_attempted"]).To(gomega.BeFalse(), fmt.Sprintf("unexpected freshness path/refresh telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_refresh_skipped_reason"]).To(gomega.Equal("cap exceeded"), fmt.Sprintf("unexpected freshness path/refresh telemetry fields: %#v", fields))
		gomega.Expect(fields["context_stale_evidence_state"]).To(gomega.Equal("blocked"), fmt.Sprintf("unexpected stale-evidence telemetry fields: %#v", fields))
		gomega.Expect(fields["context_stale_use_blocked"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected stale-evidence telemetry fields: %#v", fields))
		gomega.Expect(fields["context_stale_evidence_stale_path_count"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected stale-evidence telemetry fields: %#v", fields))
		gomega.Expect(fields["context_stale_file_count"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected structured stale count telemetry fields: %#v", fields))
		gomega.Expect(fields["context_missing_file_count"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected structured stale count telemetry fields: %#v", fields))
		gomega.Expect(fields["context_stale_path_count"]).To(gomega.BeNumerically("==", 3), fmt.Sprintf("unexpected structured stale count telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_checked_path_count"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected structured stale count telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_stale_path_count"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected structured stale count telemetry fields: %#v", fields))
		gomega.Expect(fields["context_freshness_missing_path_count"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected structured stale count telemetry fields: %#v", fields))
		gomega.Expect(fields["context_confidence_level"]).To(gomega.Equal("low"), fmt.Sprintf("unexpected confidence telemetry fields: %#v", fields))
		gomega.Expect(fields["context_confidence_source"]).To(gomega.Equal("freshness_decision"), fmt.Sprintf("unexpected confidence telemetry fields: %#v", fields))
		gomega.Expect(fields["context_confidence_reason"]).To(gomega.Equal("budget_exceeded"), fmt.Sprintf("unexpected confidence telemetry fields: %#v", fields))
		gomega.Expect(fields["context_provenance_source"]).To(gomega.Equal("context_graph"), fmt.Sprintf("unexpected provenance telemetry fields: %#v", fields))
		gomega.Expect(fields["context_provenance_operation"]).To(gomega.Equal("context_pack"), fmt.Sprintf("unexpected provenance telemetry fields: %#v", fields))
		gomega.Expect(fields["context_provenance_index_id"]).To(gomega.Equal("idx123"), fmt.Sprintf("unexpected provenance telemetry fields: %#v", fields))
		gomega.Expect(fields["context_provenance_artifact_path"]).To(gomega.Equal(".idiom/comprehension/views/abc.json"), fmt.Sprintf("unexpected provenance telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_state"]).To(gomega.Equal("hit"), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_source"]).To(gomega.Equal("graph_views"), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_key"]).To(gomega.Equal("abc"), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_workspace_state_fingerprint"]).To(gomega.Equal("fp123"), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_ranker_weights_fingerprint"]).To(gomega.Equal("ranker123"), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_runtime_traces_fingerprint"]).To(gomega.Equal("trace123"), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_read_through_refresh"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_invalidation_policy"]).To(gomega.Equal("content_addressed_read_through"), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_cache_invalidation_sources"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))
		gomega.Expect(fields["context_unresolved_gap_details"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected cache/gap telemetry fields: %#v", fields))

		names, ok := fields["context_collector_report_names"].([]string)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing collector report names: %#v", fields))
		gomega.Expect(names).To(gomega.ContainElement("python_lsp"), fmt.Sprintf("missing collector report names: %#v", fields))
		gomega.Expect(names).To(gomega.ContainElement("workspace_tool_enrichment"), fmt.Sprintf("missing collector report names: %#v", fields))

		coverage, ok := fields["context_collector_coverage_names"].([]string)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing collector coverage names: %#v", fields))
		gomega.Expect(coverage).To(gomega.ContainElement("python_index"), fmt.Sprintf("missing collector coverage names: %#v", fields))

	})
	ginkgo.It("context telemetry fields include bounded freshness path samples", func() {
		fields := ContextTelemetryFields("context_pack", `{
  "freshness": {
    "policy": "read_through",
    "fresh_for_scope": false,
    "stale_path_count": 6,
    "missing_path_count": 2,
    "stale_paths": ["pkg/a.go", "pkg/b.go", "pkg/c.go", "pkg/d.go", "pkg/e.go", "pkg/f.go"],
    "missing_paths": ["pkg/missing.go", "", "pkg/generated.go"]
  }
}`)

		staleSamples, ok := fields["context_freshness_stale_path_samples"].([]string)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("unexpected stale path samples: %#v", fields))
		gomega.Expect(reflect.DeepEqual(staleSamples, []string{"pkg/a.go", "pkg/b.go", "pkg/c.go", "pkg/d.go", "pkg/e.go"})).To(gomega.BeTrue(), fmt.Sprintf("unexpected stale path samples: %#v", fields))

		missingSamples, ok := fields["context_freshness_missing_path_samples"].([]string)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("unexpected missing path samples: %#v", fields))
		gomega.Expect(reflect.DeepEqual(missingSamples, []string{"pkg/missing.go", "pkg/generated.go"})).To(gomega.BeTrue(), fmt.Sprintf("unexpected missing path samples: %#v", fields))
		gomega.Expect(fields["context_freshness_stale_paths"]).To(gomega.BeNumerically("==", 6), fmt.Sprintf("unexpected freshness path counts: %#v", fields))
		gomega.Expect(fields["context_freshness_missing_paths"]).To(gomega.BeNumerically("==", 3), fmt.Sprintf("unexpected freshness path counts: %#v", fields))
		gomega.Expect(fields["context_freshness_stale_path_count"]).To(gomega.BeNumerically("==", 6), fmt.Sprintf("unexpected freshness path counts: %#v", fields))
		gomega.Expect(fields["context_freshness_missing_path_count"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected freshness path counts: %#v", fields))

	})
	ginkgo.It("workspace tool enrichment normalizes relationship edges", func() {
		graph := comprehension.Graph{
			Root: "/workspace",
			Nodes: []comprehension.Node{
				{ID: "file://app.py", Kind: "file", Path: "app.py", Language: "python"},
				{ID: "file://lib.py", Kind: "file", Path: "lib.py", Language: "python"},
				{ID: "symbol://app.py#run", Kind: "func", Path: "app.py", Language: "python", QualifiedName: "run", StartLine: 1, EndLine: 3, Properties: map[string]any{"name": "run"}},
				{ID: "symbol://lib.py#helper", Kind: "func", Path: "lib.py", Language: "python", QualifiedName: "helper", StartLine: 1, EndLine: 1, Properties: map[string]any{"name": "helper"}},
				{ID: "symbol://lib.py#caller", Kind: "func", Path: "lib.py", Language: "python", QualifiedName: "caller", StartLine: 3, EndLine: 5, Properties: map[string]any{"name": "caller"}},
			},
		}
		lookup := newGraphRelationshipLookup(graph)
		subject := graph.Nodes[2]

		outgoing := normalizeToolRelationshipEdges(graph, lookup, subject, "calls_from", "python_calls_from", ToolResult{Output: "app.py:2 -> helper in lib.py:1", PrecisionTier: PrecisionTierLSP})
		gomega.Expect(hasWorkspaceRelationshipEdge(outgoing, "calls", "symbol://app.py#run", "symbol://lib.py#helper")).To(gomega.BeTrue(), fmt.Sprintf("expected normalized outgoing call edge, got %#v", outgoing))

		incoming := normalizeToolRelationshipEdges(graph, lookup, graph.Nodes[3], "calls_into", "python_calls_into", ToolResult{Output: "lib.py:3 caller", PrecisionTier: PrecisionTierSyntaxIndex})
		gomega.Expect(hasWorkspaceRelationshipEdge(incoming, "calls", "symbol://lib.py#caller", "symbol://lib.py#helper")).To(gomega.BeTrue(), fmt.Sprintf("expected normalized incoming call edge, got %#v", incoming))

		references := normalizeToolRelationshipEdges(graph, lookup, graph.Nodes[3], "references", "python_references", ToolResult{Output: "lib.py:3:5 caller()", PrecisionTier: PrecisionTierSyntaxIndex})
		gomega.Expect(hasWorkspaceRelationshipEdge(references, "references", "symbol://lib.py#caller", "symbol://lib.py#helper")).To(gomega.BeTrue(), fmt.Sprintf("expected normalized reference edge, got %#v", references))

		typed := normalizeToolRelationshipEdges(graph, lookup, subject, "calls_from", "python_calls_from", ToolResult{
			PrecisionTier: PrecisionTierLSP,
			Relationships: []ToolRelationship{{
				Kind:         "calls",
				SourcePath:   "app.py",
				SourceLine:   2,
				SourceSymbol: "run",
				TargetPath:   "lib.py",
				TargetLine:   1,
				TargetSymbol: "helper",
				Evidence:     "typed relationship payload",
			}},
		})
		gomega.Expect(hasWorkspaceRelationshipEdge(typed, "calls", "symbol://app.py#run", "symbol://lib.py#helper")).To(gomega.BeTrue(), fmt.Sprintf("expected structured relationship payload to normalize, got %#v", typed))

	})
	ginkgo.It("language tool results expose structured relationships", func() {
		result := annotateLanguageRelationships(ToolResult{
			Tool:   "python_calls_from",
			Output: "app.py:2 -> helper in lib.py:1",
		})
		gomega.Expect(result.Relationships).To(gomega.HaveLen(1), fmt.Sprintf("expected parsed relationship payload, got %#v", result))

		relationship := result.Relationships[0]
		gomega.Expect(relationship.Kind).To(gomega.Equal("calls"), fmt.Sprintf("unexpected relationship payload: %#v", relationship))
		gomega.Expect(relationship.SourcePath).To(gomega.Equal("app.py"), fmt.Sprintf("unexpected relationship payload: %#v", relationship))
		gomega.Expect(relationship.SourceLine).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected relationship payload: %#v", relationship))
		gomega.Expect(relationship.TargetPath).To(gomega.Equal("lib.py"), fmt.Sprintf("unexpected relationship payload: %#v", relationship))
		gomega.Expect(relationship.TargetLine).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected relationship payload: %#v", relationship))
		gomega.Expect(relationship.TargetSymbol).To(gomega.Equal("helper"), fmt.Sprintf("unexpected relationship payload: %#v", relationship))

	})
	ginkgo.It("language tool results normalize backslash relationship paths", func() {
		result := annotateLanguageRelationships(ToolResult{
			Tool:   "python_calls_from",
			Output: `src\app.py:2 -> helper in src\lib.py:1`,
		})
		gomega.Expect(result.Relationships).To(gomega.HaveLen(1), fmt.Sprintf("expected parsed relationship payload, got %#v", result))

		relationship := result.Relationships[0]
		gomega.Expect(relationship.SourcePath).To(gomega.Equal("src/app.py"), fmt.Sprintf("expected slash-normalized relationship paths, got %#v", relationship))
		gomega.Expect(relationship.TargetPath).To(gomega.Equal("src/lib.py"), fmt.Sprintf("expected slash-normalized relationship paths, got %#v", relationship))

	})
	ginkgo.It("dependency graph extracts type script imports without madge", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "src", "main.ts"), `import {x} from "./lib";
const fs = require("fs");
export { y } from "./other";
`)
		writeWorkspaceFile(filepath.Join(root, "src", "module.mts"), `import {x} from "./lib";
export {x};
`)
		writeWorkspaceFile(filepath.Join(root, "src", "common.cts"), `const lib = require("./lib");
module.exports = lib;
`)
		writeWorkspaceFile(filepath.Join(root, "src", "lib.ts"), `export const x = 1;`)

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})
		target := filepath.Join(root, "src")
		result := mgr.regexDependencyGraph(target, "typescript", typescriptDependencyGraphExtensions(), tsImportRe)
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("dependency_graph returned error: %v\n%s", result.Error, result.Output))

		for _, want := range []string{"src/main.ts -> ./lib", "src/main.ts -> ./other", "src/main.ts -> fs", "src/module.mts -> ./lib", "src/common.cts -> ./lib"} {
			gomega.Expect(result.Output).To(gomega.ContainSubstring(want), fmt.Sprintf("expected dependency graph to include %q, got:\n%s", want, result.Output))

		}
	})
	ginkgo.It("infer dependency language recognizes type script module extensions", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "src", "entry.mts"), `export const entry = true;`)

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxDirectoryEntries: 16})
		if got := mgr.inferDependencyLanguage(filepath.Join(root, "src")); got != "typescript" {
			ginkgo.Fail(fmt.Sprintf("inferDependencyLanguage(.mts workspace) = %q, want typescript", got))
		}
	})
	ginkgo.It("dependency graph extracts python imports without pydeps", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "pkg", "main.py"), `import os, sys
from pkg.helpers import run
`)

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})
		target := filepath.Join(root, "pkg")
		result := mgr.regexDependencyGraph(target, "python", []string{".py", ".pyi"}, pythonImportRe)
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("dependency_graph returned error: %v\n%s", result.Error, result.Output))

		for _, want := range []string{"pkg/main.py -> os", "pkg/main.py -> sys", "pkg/main.py -> pkg.helpers"} {
			gomega.Expect(result.Output).To(gomega.ContainSubstring(want), fmt.Sprintf("expected dependency graph to include %q, got:\n%s", want, result.Output))

		}
	})
	ginkgo.It("history and blame tools use git", func() {
		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip("git is not installed")
		}

		root := ginkgo.GinkgoT().TempDir()
		runTestCommand(root, "git", "init")
		runTestCommand(root, "git", "config", "user.email", "test@example.com")
		runTestCommand(root, "git", "config", "user.name", "Test User")
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a + b\n}\n")
		runTestCommand(root, "git", "add", "sample.go")
		runTestCommand(root, "git", "commit", "-m", "initial")
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a - b\n}\n")
		runTestCommand(root, "git", "commit", "-am", "change add")

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})

		history := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 3, EndLine: 5})
		gomega.Expect(history.Error).To(gomega.Equal(""), fmt.Sprintf("history_range returned error: %v\n%s", history.Error, history.Output))
		gomega.Expect(history.Code).To(gomega.Equal(""), fmt.Sprintf("tracked history_range Code = %q, want empty; result=%#v", history.Code, history))
		gomega.Expect(history.Output).To(gomega.ContainSubstring("change add"), fmt.Sprintf("expected git log -L output to include both commits, got:\n%s", history.Output))
		gomega.Expect(history.Output).To(gomega.ContainSubstring("initial"), fmt.Sprintf("expected git log -L output to include both commits, got:\n%s", history.Output))

		pathOnlyHistory := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go"})
		gomega.Expect(pathOnlyHistory.Error).To(gomega.Equal(""), fmt.Sprintf("path-only history_range returned error: %v\n%s", pathOnlyHistory.Error, pathOnlyHistory.Output))
		gomega.Expect(pathOnlyHistory.Code).To(gomega.Equal(""), fmt.Sprintf("path-only history_range Code = %q, want empty; result=%#v", pathOnlyHistory.Code, pathOnlyHistory))
		gomega.Expect(pathOnlyHistory.Summary).To(gomega.ContainSubstring("lines 1-5"), fmt.Sprintf("expected path-only history_range to default to bounded file lines, got summary %q", pathOnlyHistory.Summary))
		gomega.Expect(pathOnlyHistory.Output).To(gomega.ContainSubstring("change add"), fmt.Sprintf("expected path-only git log -L output to include both commits, got:\n%s", pathOnlyHistory.Output))
		gomega.Expect(pathOnlyHistory.Output).To(gomega.ContainSubstring("initial"), fmt.Sprintf("expected path-only git log -L output to include both commits, got:\n%s", pathOnlyHistory.Output))

		symbolHistory := mgr.Execute(context.Background(), ToolRequest{Tool: "history_symbol", Path: "sample.go", Pattern: "add"})
		gomega.Expect(symbolHistory.Error).To(gomega.Equal(""), fmt.Sprintf("history_symbol returned error: %v\n%s", symbolHistory.Error, symbolHistory.Output))
		gomega.Expect(symbolHistory.Code).To(gomega.Equal(""), fmt.Sprintf("tracked history_symbol Code = %q, want empty; result=%#v", symbolHistory.Code, symbolHistory))
		gomega.Expect(symbolHistory.Output).To(gomega.ContainSubstring("change add"), fmt.Sprintf("expected git log -L symbol output to include both commits, got:\n%s", symbolHistory.Output))
		gomega.Expect(symbolHistory.Output).To(gomega.ContainSubstring("initial"), fmt.Sprintf("expected git log -L symbol output to include both commits, got:\n%s", symbolHistory.Output))

		blame := mgr.Execute(context.Background(), ToolRequest{Tool: "blame_range", Path: "sample.go", StartLine: 4, EndLine: 4})
		gomega.Expect(blame.Error).To(gomega.Equal(""), fmt.Sprintf("blame_range returned error: %v\n%s", blame.Error, blame.Output))
		gomega.Expect(blame.Output).To(gomega.ContainSubstring("return a - b"), fmt.Sprintf("expected blame output for changed line, got:\n%s", blame.Output))

	})
	ginkgo.It("history range reports not applicable for non git root", func() {
		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip("git is not installed")
		}

		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n")

		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

		assertHistoryNotApplicable(result, "non_git_root", "sample.go")
	})
	ginkgo.It("history range reports not applicable for git repo without head", func() {
		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip("git is not installed")
		}

		root := ginkgo.GinkgoT().TempDir()
		runTestCommand(root, "git", "init")
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n")

		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

		assertHistoryNotApplicable(result, "no_head", "sample.go")
	})
	ginkgo.It("history range reports not applicable for untracked git path", func() {
		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip("git is not installed")
		}

		root := ginkgo.GinkgoT().TempDir()
		runTestCommand(root, "git", "init")
		runTestCommand(root, "git", "config", "user.email", "test@example.com")
		runTestCommand(root, "git", "config", "user.name", "Test User")
		writeWorkspaceFile(filepath.Join(root, "README.md"), "baseline\n")
		runTestCommand(root, "git", "add", "README.md")
		runTestCommand(root, "git", "commit", "-m", "initial")
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n")

		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

		assertHistoryNotApplicable(result, "untracked_path", "sample.go")
	})
	ginkgo.It("history range reports not applicable for tracked path without committed history", func() {
		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip("git is not installed")
		}

		root := ginkgo.GinkgoT().TempDir()
		runTestCommand(root, "git", "init")
		runTestCommand(root, "git", "config", "user.email", "test@example.com")
		runTestCommand(root, "git", "config", "user.name", "Test User")
		writeWorkspaceFile(filepath.Join(root, "README.md"), "baseline\n")
		runTestCommand(root, "git", "add", "README.md")
		runTestCommand(root, "git", "commit", "-m", "initial")
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n")
		runTestCommand(root, "git", "add", "sample.go")

		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

		assertHistoryNotApplicable(result, "path_no_committed_history", "sample.go")
	})
	ginkgo.It("history range rejects partial range", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n")

		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("partial history_range should be rejected, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("start_line and end_line are required"), fmt.Sprintf("partial history_range should explain required bounds, got %#v", result))

	})
	ginkgo.It("history range missing path remains error", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "missing.go", StartLine: 1, EndLine: 1})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("missing path should remain an error, got %#v", result))
		gomega.Expect(result.Code).NotTo(gomega.Equal(HistoryNotApplicableCode), fmt.Sprintf("missing path must not be reported as not applicable: %#v", result))

	})
	ginkgo.It("history range missing git binary remains error", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n")
		ginkgo.GinkgoT().Setenv("PATH", "")

		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("missing git binary should remain an error, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("git is not installed"), fmt.Sprintf("missing git binary should remain an error, got %#v", result))
		gomega.Expect(result.Code).NotTo(gomega.Equal(HistoryNotApplicableCode), fmt.Sprintf("missing git binary must not be reported as not applicable: %#v", result))

	})
	ginkgo.It("history symbol malformed pattern remains error", func() {
		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip("git is not installed")
		}

		root := ginkgo.GinkgoT().TempDir()
		runTestCommand(root, "git", "init")
		runTestCommand(root, "git", "config", "user.email", "test@example.com")
		runTestCommand(root, "git", "config", "user.name", "Test User")
		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int { return a + b }\n")
		runTestCommand(root, "git", "add", "sample.go")
		runTestCommand(root, "git", "commit", "-m", "initial")

		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_symbol", Path: "sample.go", Pattern: "[["})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("malformed history_symbol pattern should remain an error, got %#v", result))
		gomega.Expect(result.Code).NotTo(gomega.Equal(HistoryNotApplicableCode), fmt.Sprintf("malformed history_symbol pattern must not be reported as not applicable: %#v", result))

	})
	ginkgo.It("history range does not attach commit facts on git failure", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.Mkdir(filepath.Join(root, ".git"), 0o755)).To(gomega.Succeed())

		writeWorkspaceFile(filepath.Join(root, "sample.go"), "package main\n")

		bin := ginkgo.GinkgoT().TempDir()
		fakeGit := filepath.Join(bin, "git")
		script := `#!/bin/sh
case "$1" in
  rev-parse)
    case "$2" in
      --git-dir) echo .git; exit 0 ;;
      --verify) echo abc123; exit 0 ;;
    esac
    ;;
  ls-files)
    echo sample.go
    exit 0
    ;;
  ls-tree)
    echo sample.go
    exit 0
    ;;
  log)
    echo "commit abc123"
    echo
    echo "    partial output before failure"
    exit 1
    ;;
esac
exit 2
`
		gomega.Expect(os.WriteFile(fakeGit, []byte(script), 0o755)).To(gomega.Succeed())
		ginkgo.GinkgoT().Setenv("PATH", bin)

		mgr := historyTestManager(root)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected failing fake git log to remain an error, got %#v", result))
		gomega.Expect(result.Facts).To(gomega.BeNil(), fmt.Sprintf("failing git history command must not attach authoritative facts: %#v", result.Facts))

	})
})

func historyTestManager(root string) *Manager {
	return NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})
}

func assertHistoryNotApplicable(result ToolResult, reason string, path string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("history result should be non-error not_applicable, got error=%q output=%s", result.Error, result.Output))
	gomega.Expect(result.Code).To(gomega.Equal(HistoryNotApplicableCode), fmt.Sprintf("history result Code = %q, want %q; result=%#v", result.Code, HistoryNotApplicableCode, result))
	gomega.Expect(result.Facts).NotTo(gomega.BeNil(), fmt.Sprintf("history not-applicable facts = %#v, want path=%q", result.Facts, path))
	gomega.Expect(result.Facts.GitHistoryPath).To(gomega.Equal(path), fmt.Sprintf("history not-applicable facts = %#v, want path=%q", result.Facts, path))
	gomega.Expect(result.Facts.GitHistoryStatus).To(gomega.Equal(toolfacts.GitHistoryStatusNotApplicable), fmt.Sprintf("history not-applicable facts = %#v, want path=%q", result.Facts, path))

	var payload struct {
		Status string `json:"status"`
		Code   string `json:"code"`
		Reason string `json:"reason"`
		Path   string `json:"path"`
	}
	gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
	gomega.Expect(payload.Status).To(gomega.Equal("not_applicable"), fmt.Sprintf("unexpected history not-applicable payload: %#v", payload))
	gomega.Expect(payload.Code).To(gomega.Equal(HistoryNotApplicableCode), fmt.Sprintf("unexpected history not-applicable payload: %#v", payload))
	gomega.Expect(payload.Reason).To(gomega.Equal(reason), fmt.Sprintf("unexpected history not-applicable payload: %#v", payload))
	gomega.Expect(payload.Path).To(gomega.Equal(path), fmt.Sprintf("unexpected history not-applicable payload: %#v", payload))

}

var _ = ginkgo.Describe("discovery tools", func() {
	ginkgo.It("structural search rejects missing pattern", func() {
		mgr := NewManager(testWorkspaceConfig())
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "structural_search", Path: "."})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected missing-pattern error, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("pattern is required"), fmt.Sprintf("expected missing-pattern error, got %#v", result))

	})
	ginkgo.It("structural search facts use ast grep files", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "internal", "runner.go"), "package internal\n")
		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		output := fmt.Sprintf(`[
  {"file":%q,"range":{"start":{"line":1,"column":1},"end":{"line":3,"column":1}}},
  {"file":"pkg/handler.go","range":{"start":{"line":10,"column":1},"end":{"line":12,"column":1}}},
  {"file":"pkg/handler.go","range":{"start":{"line":20,"column":1},"end":{"line":22,"column":1}}}
]`, filepath.Join(root, "internal", "runner.go"))

		facts := mgr.structuralSearchToolResultFacts(output)
		gomega.Expect(facts).NotTo(gomega.BeNil(), "expected structural_search facts")

		if got, want := facts.CandidatePaths, []string{"internal/runner.go", "pkg/handler.go"}; !slices.Equal(got, want) {
			ginkgo.Fail(fmt.Sprintf("candidate paths = %#v, want %#v", got, want))
		}
		gomega.Expect(facts.HasDiscoveryTargets).To(gomega.BeTrue(), fmt.Sprintf("unexpected structural_search facts: %#v", facts))
		gomega.Expect(facts.DiscoveryStrength).To(gomega.Equal(toolfacts.DiscoveryStrengthSourceOwner), fmt.Sprintf("unexpected structural_search facts: %#v", facts))
		gomega.Expect(facts.SourceOwnerFound).To(gomega.BeTrue(), fmt.Sprintf("unexpected structural_search facts: %#v", facts))

	})
	ginkgo.It("optional external discovery tools report unavailable", func() {
		ginkgo.GinkgoT().Setenv("PATH", "")

		mgr := NewManager(testWorkspaceConfig())

		for _, req := range []ToolRequest{
			{Tool: "structural_search", Path: ".", Pattern: "$X"},
			{Tool: "ctags_symbols", Path: "."},
			{Tool: "semgrep_scan", Path: "."},
			{Tool: "joern_scan", Path: "."},
		} {
			result := mgr.Execute(context.Background(), req)
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected %s to report unavailable, got %#v", req.Tool, result))
			gomega.Expect(result.Summary).To(gomega.ContainSubstring("unavailable"), fmt.Sprintf("expected %s to report unavailable, got %#v", req.Tool, result))

		}
	})
})

func assertNoPrescriptiveGraphOutput(name string, output string) {
	ginkgo.GinkgoHelper()

	lower := strings.ToLower(output)
	for _, forbidden := range []string{
		"run context_index",
		"run context_pack",
		"rerun context_trace_pack",
		"use context_pack",
		"provide a bounded path",
		"identify one exact",
		"language-aware symbol tool",
		"before relying",
		"next action",
		"next step",
		"invoke",
	} {
		gomega.Expect(lower).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("%s output contains prescriptive phrase %q:\n%s", name, forbidden, output))

	}
}

func writeWorkspaceFile(path string, content string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())

}

func mustMarshalTestJSON(value any) string {
	ginkgo.GinkgoHelper()

	data, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal JSON: %v", err))

	return string(data)
}

func packReportSourcePresent(sources []struct {
	Source string `json:"source"`
	State  string `json:"state"`
}, source string) bool {

	for _, item := range sources {
		if item.Source == source {
			return true
		}
	}

	return false
}

func packReportRankerReasonPresent(reasons []struct {
	Reason string `json:"reason"`
	State  string `json:"state"`
}, reason string) bool {

	for _, item := range reasons {
		if item.Reason == reason {
			return true
		}
	}

	return false
}

func runTestCommand(dir string, name string, args ...string) {
	ginkgo.GinkgoHelper()

	cmd := exec.Command(name, args...)

	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(output)))

}

func hasWorkspaceRelationshipEdge(edges []comprehension.Edge, kind string, sourceID string, targetID string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.SourceID == sourceID && edge.TargetID == targetID {
			return true
		}
	}

	return false
}

var _ = ginkgo.Describe("discovery tools", func() {
	ginkgo.It("harness audit reports context cache state", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/cache-audit\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
		// Seed an empty conversation log so the behavior-purpose audit can run.
		writeWorkspaceFile(filepath.Join(root, "logs", "conversations", "seed.jsonl"), "")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		// Populate at least one cache row so the audit has something to report.
		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "audit cache exercise", Mode: "plan"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_pack returned error: %v\n%s", result.Error, result.Output))
		}

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.ContextCache).NotTo(gomega.BeNil(), fmt.Sprintf("expected context_cache section in audit report, got %s", result.Output))
		gomega.Expect(report.ContextCache.Rows < 1).To(gomega.BeFalse(), fmt.Sprintf("expected at least one graph_views row after context_pack, got %d", report.ContextCache.Rows))
		gomega.Expect(report.ContextCache.SessionWrites < 1).To(gomega.BeFalse(), fmt.Sprintf("expected at least one session write counter, got %+v", report.ContextCache))
		gomega.Expect(report.ContextCache.ContractRows < 1).To(gomega.BeFalse(), fmt.Sprintf("expected graph-view contract row count in cache audit, got %+v", report.ContextCache))
		gomega.Expect(report.FreshnessHealth).NotTo(gomega.BeNil(), fmt.Sprintf("expected freshness health counters in audit report, got %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.ReadThroughAttempts).NotTo(gomega.BeNumerically("<", 1), fmt.Sprintf("expected freshness health counters in audit report, got %#v", report.FreshnessHealth))

		// Confirm the comprehension package's view stats type is the same shape
		// the audit wraps; this catches drift if either side adds a field
		// without updating the mirror.
		stats, err := comprehension.ReadViewStats(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadViewStats: %v", err))
		gomega.Expect(stats.Rows).To(gomega.Equal(report.ContextCache.Rows), fmt.Sprintf("audit row count drifted from comprehension stats: audit=%d stats=%d", report.ContextCache.Rows, stats.Rows))

	})
	ginkgo.It("harness audit freshness health includes log derived telemetry", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "logs", "conversations", "freshness.jsonl"), strings.Join([]string{
			`{"type":"tool_result","tool":"context_query","context_freshness_policy":"read_through","context_freshness_blocked":true,"context_freshness_budget_exceeded":true,"context_freshness_refresh_attempted":true,"context_freshness_refresh_error":"refresh failed","context_freshness_refresh_files_indexed":3,"context_stale_evidence_state":"blocked","context_stale_use_blocked":true}`,
			`{"type":"tool_result","tool":"context_zoom","context_freshness_policy":"report_only","context_stale_evidence_state":"reported","context_stale_use_reported":true}`,
			`{"type":"tool_result","tool":"context_status","context_freshness_policy":"report_only","context_stale_evidence_state":"fresh","context_stale_use_fresh":true}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.FreshnessHealth).NotTo(gomega.BeNil(), fmt.Sprintf("expected freshness_health section, got %s", result.Output))
		gomega.Expect(report.FreshnessHealth.TelemetryReadThrough).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected log-derived freshness health: %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.TelemetryBlockers).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected log-derived freshness health: %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.TelemetryBudgetExceeded).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected log-derived freshness health: %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.TelemetryRefreshFailures).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected log-derived freshness health: %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.TelemetryRefreshFiles).To(gomega.BeNumerically("==", 3), fmt.Sprintf("unexpected log-derived freshness health: %#v", report.FreshnessHealth))

		if _, ok := report.FreshnessHealth.TelemetryPolicyCounts["read_through"]; !ok {
			ginkgo.Fail(fmt.Sprintf("expected policy counts to include read_through: %#v", report.FreshnessHealth.TelemetryPolicyCounts))
		}
		gomega.Expect(report.FreshnessHealth.StaleUseBlocked).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected stale-use blocker coverage in freshness health, got %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.StaleUseReported).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected stale-use blocker coverage in freshness health, got %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.StaleUseFresh).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected stale-use blocker coverage in freshness health, got %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.StaleUseStateCounts["blocked"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected stale-use blocker coverage in freshness health, got %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.StaleUseStateCounts["reported"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected stale-use blocker coverage in freshness health, got %#v", report.FreshnessHealth))
		gomega.Expect(report.FreshnessHealth.StaleUseStateCounts["fresh"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected stale-use blocker coverage in freshness health, got %#v", report.FreshnessHealth))

	})
})
