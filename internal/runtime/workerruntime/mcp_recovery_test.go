package workerruntime

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
	recoverycontract "idiom/internal/contracts/recovery"
)

var _ = ginkgo.Describe("MCP recovery", func() {
	ginkgo.It("classifies schema failures with the concrete missing argument and keeps the MCP tool callable", func() {
		req := workspace.ToolRequest{Tool: "mcp:exa/web_search_exa"}
		result := workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "MCP tool reported an error",
			Error:   "required query",
		}

		guidance := BuildActionGuidance(ActionGuidanceInput{Request: req, Result: result})
		plan := BuildRecoveryPlan(RecoveryInput{Request: req, Result: result})

		gomega.Expect(ClassifyFailure("tool_result", req, result)).To(gomega.Equal(recoverycontract.FailureClassMCPInputError))
		gomega.Expect(plan.FailureClass).To(gomega.Equal(recoverycontract.FailureClassMCPInputError))

		valid := strings.Join(guidance.ValidNext, "\n")
		gomega.Expect(valid).To(gomega.ContainSubstring("mcp:exa/web_search_exa"))
		gomega.Expect(valid).To(gomega.ContainSubstring("required query"))
		gomega.Expect(valid).To(gomega.ContainSubstring("corrected MCP input"))
		gomega.Expect(valid).To(gomega.ContainSubstring("declared MCP schema"))
		gomega.Expect(valid).NotTo(gomega.ContainSubstring("search_content"))
		gomega.Expect(valid).NotTo(gomega.ContainSubstring("workspace search"))

		recovery := strings.Join(append(append([]string{}, plan.DoNotRepeat...), plan.RequiredNext...), "\n")
		gomega.Expect(recovery).To(gomega.ContainSubstring("same mcp:exa/web_search_exa request with the same invalid MCP input"))
		gomega.Expect(recovery).To(gomega.ContainSubstring("required query"))
		gomega.Expect(recovery).To(gomega.ContainSubstring("retry that MCP tool"))
	})

	ginkgo.It("classifies non-schema MCP failures without replacing the callable tool with workspace recovery", func() {
		req := workspace.ToolRequest{Tool: "mcp:exa/web_fetch_exa"}
		result := workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "MCP call failed",
			Error:   "upstream connection reset",
		}

		guidance := BuildActionGuidance(ActionGuidanceInput{Request: req, Result: result})
		plan := BuildRecoveryPlan(RecoveryInput{Request: req, Result: result})

		gomega.Expect(ClassifyFailure("tool_result", req, result)).To(gomega.Equal(recoverycontract.FailureClassMCPToolFailure))
		gomega.Expect(plan.FailureClass).To(gomega.Equal(recoverycontract.FailureClassMCPToolFailure))

		valid := strings.Join(guidance.ValidNext, "\n")
		gomega.Expect(valid).To(gomega.ContainSubstring("retry the callable MCP tool mcp:exa/web_fetch_exa"))
		gomega.Expect(valid).NotTo(gomega.ContainSubstring("search_content"))
		gomega.Expect(valid).NotTo(gomega.ContainSubstring("search_files"))
	})
})
