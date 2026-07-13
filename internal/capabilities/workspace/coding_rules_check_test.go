package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	rulescontract "idiom/internal/contracts/codingrules"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("coding rules check", func() {
	ginkgo.It("coding rules check returns structured report", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, ".idiom", "coding_rules.json"), `{
  "schema_version": "idiom.coding_rules.v1",
  "defaults": {
    "enforcement": "blocking",
    "severity": "error"
  },
  "rules": [
    {
      "id": "go.no_pass_through_wrapper",
      "languages": ["go"],
      "kind": "structure"
    }
  ]
}`)
		writeTestFile(filepath.Join(root, "internal", "sample", "wrapper.go"), `package sample

type store struct{}

func (store) Save(ctx string, user string) error { return nil }

var repo store

func Save(ctx string, user string) error {
	return repo.Save(ctx, user)
}
`)

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: ToolNameCodingRulesCheck,
			Path: filepath.ToSlash(filepath.Join("internal", "sample")),
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("coding_rules_check error: %s", result.Error))
		gomega.Expect(result.PayloadType).To(gomega.Equal(codingRulesReportPayloadType), fmt.Sprintf("payload type = %q, want %q", result.PayloadType, codingRulesReportPayloadType))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("fail"), fmt.Sprintf("summary does not report blocking failure: %q", result.Summary))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("blocking=1"), fmt.Sprintf("summary does not report blocking failure: %q", result.Summary))

		var report rulescontract.Report
		gomega.Expect(json.Unmarshal(result.Payload, &report)).To(gomega.Succeed())
		gomega.Expect(report.Status).To(gomega.Equal(rulescontract.StatusFail), fmt.Sprintf("unexpected report: %#v", report))
		gomega.Expect(report.Summary.Blocking).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected report: %#v", report))
		gomega.Expect(report.Findings).To(gomega.HaveLen(1), fmt.Sprintf("unexpected report: %#v", report))

		finding := report.Findings[0]
		gomega.Expect(finding.RuleID).To(gomega.Equal(rulescontract.RuleNoPassThroughWrapper), fmt.Sprintf("unexpected finding: %#v", finding))
		gomega.Expect(finding.Symbol).To(gomega.Equal("Save"), fmt.Sprintf("unexpected finding: %#v", finding))
		gomega.Expect(finding.Hash).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected finding: %#v", finding))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("return repo.Save"), fmt.Sprintf("coding rules report must not include raw source bodies: %s", result.Output))

	})
})
