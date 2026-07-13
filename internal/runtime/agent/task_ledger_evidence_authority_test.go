package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/taskledger"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
)

var _ = ginkgo.Describe("Task ledger evidence authority", func() {
	completedLedgerUpdate := func(evidence string) workspace.ToolRequest {
		return workspace.ToolRequest{
			Tool: workspace.ToolNameTaskLedger,
			Todos: []workspace.TaskLedgerItem{{
				ID:       "work",
				Content:  "complete the work",
				Status:   taskledger.StatusCompleted,
				Evidence: evidence,
			}},
		}
	}
	sourceWindowFacts := func(path string, body string, totalLines int) *toolfacts.ToolResultFacts {
		digest := toolfacts.SourceWindowSHA256([]byte(body))
		return toolfacts.NewSourceWindowFacts(toolfacts.SourceWindowEvidence{
			CanonicalID: path,
			Authority: toolfacts.SourceWindowAuthorityEvidence{
				PolicySubject:  path,
				PolicyDecision: toolpolicy.Allow,
			},
			StartLine:      1,
			EndLine:        totalLines,
			TotalLines:     totalLines,
			RevisionSHA256: digest,
			WindowSHA256:   digest,
			MutationEpoch:  1,
		})
	}

	ginkgo.It("matches read evidence only to producer-owned source-body paths", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
		read := workspace.ToolResult{
			Tool:   workspace.ToolNameReadFile,
			Output: "package sample\n// mentions secret.go without reading it\n",
			Facts:  sourceWindowFacts("internal/sample.go", "package sample\n// mentions secret.go without reading it\n", 2),
		}

		spoofed := ledger.apply(completedLedgerUpdate("read:secret.go"), []workspace.ToolResult{read})
		gomega.Expect(spoofed.Error).To(gomega.ContainSubstring("not backed"))

		backed := ledger.apply(completedLedgerUpdate("read:internal/sample.go"), []workspace.ToolResult{read})
		gomega.Expect(backed.Error).To(gomega.BeEmpty())
	})

	ginkgo.It("accepts typed diagnostic-log paths without trusting rendered output", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
		logRead := diagnosticLogReadResultForTest(
			"artifacts/runtime/app.log",
			"logs/app.log",
			1,
			1,
			1,
			false,
			"logs/app.log\nmentions logs/other.log without reading it\n",
		)

		backed := ledger.apply(completedLedgerUpdate("read:logs/app.log"), []workspace.ToolResult{logRead})
		gomega.Expect(backed.Error).To(gomega.BeEmpty())
		canonical := ledger.apply(completedLedgerUpdate("read:artifacts/runtime/app.log"), []workspace.ToolResult{logRead})
		gomega.Expect(canonical.Error).To(gomega.BeEmpty())
		spoofed := ledger.apply(completedLedgerUpdate("read:logs/other.log"), []workspace.ToolResult{logRead})
		gomega.Expect(spoofed.Error).To(gomega.ContainSubstring("not backed"))

		outputOnly := workspace.ToolResult{Tool: workspace.ToolNameReadFile, Output: "logs/app.log\nbody"}
		untyped := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
		gomega.Expect(untyped.apply(completedLedgerUpdate("read:logs/app.log"), []workspace.ToolResult{outputOnly}).Error).To(gomega.ContainSubstring("not backed"))
		for _, invalid := range []workspace.ToolResult{
			{Tool: workspace.ToolNameSearchContent, Facts: logRead.Facts},
			{Tool: workspace.ToolNameReadFile, Error: "read failed", Facts: logRead.Facts},
		} {
			fixture := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
			gomega.Expect(fixture.apply(completedLedgerUpdate("read:logs/app.log"), []workspace.ToolResult{invalid}).Error).To(gomega.ContainSubstring("not backed"))
		}
		gomega.Expect(newToolResultEvidenceProjection([]workspace.ToolResult{logRead}).HasSourceWindow("logs/app.log")).To(gomega.BeFalse(), "diagnostic metadata must never become source-body evidence")
	})

	ginkgo.It("matches verification evidence only to the exact producer command", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
		spoof := workspace.ToolResult{
			Tool:   workspace.ToolNameReadFile,
			Output: "documentation says to run go test ./...",
			Facts:  sourceWindowFacts("README.md", "documentation says to run go test ./...", 1),
		}

		unbacked := ledger.apply(completedLedgerUpdate("verify:go test ./..."), []workspace.ToolResult{spoof})
		gomega.Expect(unbacked.Error).To(gomega.ContainSubstring("not backed"))

		verified := shellCommandResultForTest("go test ./...", "ok", "")
		backed := ledger.apply(completedLedgerUpdate("verify:go test ./..."), []workspace.ToolResult{verified})
		gomega.Expect(backed.Error).To(gomega.BeEmpty())
	})

	ginkgo.It("requires an exact artifact identity from an artifact-producing result", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
		control := workspace.ToolResult{
			Tool:  workspace.ToolNameTaskLedger,
			Facts: &toolfacts.ToolResultFacts{ArtifactIDs: []string{"artifact-1"}},
		}
		unbacked := ledger.apply(completedLedgerUpdate("artifact:artifact-1"), []workspace.ToolResult{control})
		gomega.Expect(unbacked.Error).To(gomega.ContainSubstring("not backed"))

		wrongArtifact := workspace.ToolResult{
			Tool:  workspace.ToolNameDiscoveryQuery,
			Facts: &toolfacts.ToolResultFacts{ArtifactIDs: []string{"artifact-10"}},
		}
		mismatch := ledger.apply(completedLedgerUpdate("artifact:artifact-1"), []workspace.ToolResult{wrongArtifact})
		gomega.Expect(mismatch.Error).To(gomega.ContainSubstring("not backed"))

		artifact := workspace.ToolResult{
			Tool:  workspace.ToolNameDiscoveryQuery,
			Facts: &toolfacts.ToolResultFacts{ArtifactIDs: []string{"artifact-1"}},
		}
		backed := ledger.apply(completedLedgerUpdate("artifact:artifact-1"), []workspace.ToolResult{artifact})
		gomega.Expect(backed.Error).To(gomega.BeEmpty())
	})

	ginkgo.It("does not accept ChangedPaths without a producer commit receipt", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
		spoof := workspace.ToolResult{
			Tool:         workspace.ToolNameEditFile,
			ChangedPaths: []string{"internal/sample.go"},
		}
		unbacked := ledger.apply(completedLedgerUpdate("write:internal/sample.go"), []workspace.ToolResult{spoof})
		gomega.Expect(unbacked.Error).To(gomega.ContainSubstring("not backed"))
	})

	ginkgo.It("revalidates completed evidence on every update", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
		verified := shellCommandResultForTest("go test ./...", "ok", "")
		initial := ledger.apply(completedLedgerUpdate("verify:go test ./..."), []workspace.ToolResult{verified})
		gomega.Expect(initial.Error).To(gomega.BeEmpty())

		replaced := ledger.apply(completedLedgerUpdate("read:secret.go"), []workspace.ToolResult{verified})
		gomega.Expect(replaced.Error).To(gomega.ContainSubstring("not backed"))
		gomega.Expect(ledger.items[0].Evidence).To(gomega.Equal("verify:go test ./..."))
	})

	ginkgo.It("rejects delimiter-only evidence", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true, RequireEvidence: true})
		result := ledger.apply(completedLedgerUpdate(", ;\t"))

		gomega.Expect(result.Error).To(gomega.ContainSubstring("at least one typed"))
		gomega.Expect(ledger.created).To(gomega.BeFalse())
	})
})
