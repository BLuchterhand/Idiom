package app

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("busy status for prompt genericizes request", func() {
	got := busyStatusForPrompt("Can we change the Qwen is working on your request affordance?")
	gomega.Expect(got).To(gomega.Equal("Qwen is orienting around your request"), fmt.Sprintf("unexpected prompt status: %q", got))

	gomega.Expect(strings.Contains(got, "working on your request")).To(gomega.BeFalse(), fmt.Sprintf("prompt status should not use the old static busy text: %q", got))
})

var _ = ginkgo.It("busy status for search request uses generic topic", func() {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: "currentTurnBusyStatus",
			Glob:    "internal/surfaces/tui/app/*.go",
		},
	}, "")
	gomega.Expect(got).To(gomega.Equal("Qwen is searching for code related to current turn busy status"), fmt.Sprintf("unexpected search status: %q", got))
})

var _ = ginkgo.It("busy status for read request uses path topic", func() {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool: "read_file",
			Path: "internal/surfaces/tui/app/model_state.go",
		},
	}, "")
	gomega.Expect(got).To(gomega.Equal("Qwen is reading code related to model state"), fmt.Sprintf("unexpected read status: %q", got))
})

var _ = ginkgo.It("busy status for relationship request", func() {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:  "context_query",
			Query: "callers of runAgentTurn and handleToolTrace",
		},
	}, "")
	gomega.Expect(got).To(gomega.Equal("Qwen is finding intersections between run agent turn and handle tool trace"), fmt.Sprintf("unexpected relationship status: %q", got))
})

var _ = ginkgo.It("busy status for verification command", func() {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:    "shell",
			Command: "go test ./internal/surfaces/tui/app -count=1",
		},
	}, "")
	gomega.Expect(got).To(gomega.Equal("Qwen is running tests"), fmt.Sprintf("unexpected shell status: %q", got))
})

var _ = ginkgo.It("busy status does not expose full paths", func() {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool: "read_file",
			Path: "internal/surfaces/tui/app/update_handlers.go",
		},
	}, "")
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("internal/surfaces/tui/app")), fmt.Sprintf("status should genericize paths, got %q", got))
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring(".go")), fmt.Sprintf("status should genericize paths, got %q", got))
})
