package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/runprofile"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/evidence/transcript"
	"idiom/internal/runtime/agent"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

func decodeJSONEvents(t ginkgo.FullGinkgoTInterface, raw string) []map[string]any {
	ginkgo.GinkgoHelper()

	var out []map[string]any

	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}

		var record map[string]any

		{
			err := json.Unmarshal([]byte(line), &record)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode JSONL %q: %v", line, err))
		}

		out = append(out, record)
	}

	return out
}

var _ = ginkgo.It("emit json message only known roles", func() {
	t := ginkgo.GinkgoT()
	var buf bytes.Buffer

	model := &Model{}
	model.SetJSONEventStream(&buf, "sess-1")
	session := newDetachedPromptExecutionSession(context.Background(), model)

	session.eventStream.emitMessage("user", "hello")
	session.eventStream.emitMessage("assistant", "hi")
	session.eventStream.emitMessage("internal_kind", "should be filtered")

	events := decodeJSONEvents(t, buf.String())
	gomega.Expect(len(events)).To(gomega.Equal(2), fmt.Sprintf("expected 2 events, got %d (%v)", len(events), events))

	for _, e := range events {
		gomega.Expect(e["session_id"]).To(gomega.Equal("sess-1"), fmt.Sprintf("session_id missing on event: %v", e))

		gomega.Expect(e["type"]).To(gomega.Equal("message"), fmt.Sprintf("expected type=message, got %v", e["type"]))
	}

	gomega.Expect(events[0]["role"]).To(gomega.Equal("user"), fmt.Sprintf("roles wrong: %v", events))
	gomega.Expect(events[1]["role"]).To(gomega.Equal("assistant"), fmt.Sprintf("roles wrong: %v", events))
})

var _ = ginkgo.It("emit json tool request and result", func() {
	t := ginkgo.GinkgoT()
	var buf bytes.Buffer

	model := &Model{}
	model.SetJSONEventStream(&buf, "")
	session := newDetachedPromptExecutionSession(context.Background(), model)

	session.emitJSONToolRequest(agent.ToolRequestEvent{
		Step: 3, Agent: "main",
		Request: workspace.ToolRequest{Tool: "read_file", Path: "foo.go"},
		Action:  "narrow",
	})
	session.emitJSONToolResult(agent.ToolResultEvent{
		Step: 3, Agent: "main",
		Result: workspace.ToolResult{Tool: "read_file", Summary: "Read foo.go", Error: ""},
	})
	session.emitJSONToolResult(agent.ToolResultEvent{
		Step: 4, Agent: "main",
		Result: workspace.ToolResult{
			Tool:    "shell",
			Summary: "Ran cmd",
			Error:   "boom",
			Facts: &toolfacts.ToolResultFacts{
				RejectionDisposition:   toolfacts.ToolRejectionPolicyDenied,
				RejectedWritePaths:     []string{"internal/feature/target.go"},
				PendingSourceBodyPaths: []string{"internal/feature/source.go"},
			},
		},
	})

	events := decodeJSONEvents(t, buf.String())
	gomega.Expect(len(events)).To(gomega.Equal(3), fmt.Sprintf("expected 3 events, got %d", len(events)))

	gomega.Expect(events[0]["type"]).To(gomega.Equal("tool_request"), fmt.Sprintf("bad tool_request event: %v", events[0]))
	gomega.Expect(events[0]["tool"]).To(gomega.Equal("read_file"), fmt.Sprintf("bad tool_request event: %v", events[0]))
	gomega.Expect(events[0]["path"]).To(gomega.Equal("foo.go"), fmt.Sprintf("bad tool_request event: %v", events[0]))

	gomega.Expect(events[1]["type"]).To(gomega.Equal("tool_result"), fmt.Sprintf("bad tool_result event: %v", events[1]))
	gomega.Expect(events[1]["summary"]).To(gomega.Equal("Read foo.go"), fmt.Sprintf("bad tool_result event: %v", events[1]))

	{
		_, hasErr := events[1]["error"]
		gomega.Expect(hasErr).To(gomega.BeFalse(), fmt.Sprintf("successful result should omit error key: %v", events[1]))
	}

	gomega.Expect(events[2]["error"]).To(gomega.Equal("boom"), fmt.Sprintf("expected error=boom: %v", events[2]))
	gomega.Expect(events[2]["rejection_disposition"]).To(gomega.Equal("policy_denied"), fmt.Sprintf("expected typed rejection disposition: %v", events[2]))
	gomega.Expect(events[2]["rejected_write_paths"]).To(gomega.Equal([]any{"internal/feature/target.go"}), fmt.Sprintf("expected rejected path facts: %v", events[2]))
	gomega.Expect(events[2]["pending_source_body_paths"]).To(gomega.Equal([]any{"internal/feature/source.go"}), fmt.Sprintf("expected pending source facts: %v", events[2]))
})

var _ = ginkgo.It("emit json final and error", func() {
	t := ginkgo.GinkgoT()
	var buf bytes.Buffer

	model := &Model{}
	model.SetJSONEventStream(&buf, "")
	session := newDetachedPromptExecutionSession(context.Background(), model)

	session.emitJSONStart("hello prompt")
	session.eventStream.emitError("server_not_ready", "down")
	session.emitJSONFinal("done text")

	events := decodeJSONEvents(t, buf.String())
	gomega.Expect(len(events)).To(gomega.Equal(3), fmt.Sprintf("expected 3 events, got %d", len(events)))

	gomega.Expect(events[0]["type"]).To(gomega.Equal("start"), fmt.Sprintf("bad start event: %v", events[0]))
	gomega.Expect(events[0]["prompt"]).To(gomega.Equal("hello prompt"), fmt.Sprintf("bad start event: %v", events[0]))

	gomega.Expect(events[1]["type"]).To(gomega.Equal("error"), fmt.Sprintf("bad error event: %v", events[1]))
	gomega.Expect(events[1]["kind"]).To(gomega.Equal("server_not_ready"), fmt.Sprintf("bad error event: %v", events[1]))
	gomega.Expect(events[1]["message"]).To(gomega.Equal("down"), fmt.Sprintf("bad error event: %v", events[1]))

	gomega.Expect(events[2]["type"]).To(gomega.Equal("final"), fmt.Sprintf("bad final event: %v", events[2]))
	gomega.Expect(events[2]["content"]).To(gomega.Equal("done text"), fmt.Sprintf("bad final event: %v", events[2]))
})

var _ = ginkgo.It("log run event emits completion status json", func() {
	t := ginkgo.GinkgoT()
	var buf bytes.Buffer

	model := &Model{}
	model.SetJSONEventStream(&buf, "sess-1")
	session := newDetachedPromptExecutionSession(context.Background(), model)

	session.logRunEvent(agent.RunEvent{
		Role:    "completion_status",
		Content: `{"lane":"main","contract":"implementation_result","status":"blocked","blocker_kind":"required_edit_paths_remaining","edit_attempted":false}`,
	})

	events := decodeJSONEvents(t, buf.String())
	gomega.Expect(len(events)).To(gomega.Equal(1), fmt.Sprintf("expected 1 event, got %d (%v)", len(events), events))

	event := events[0]
	gomega.Expect(event["type"]).To(gomega.Equal("completion_status"), fmt.Sprintf("bad completion_status event: %v", event))
	gomega.Expect(event["status"]).To(gomega.Equal("blocked"), fmt.Sprintf("bad completion_status event: %v", event))
	gomega.Expect(event["contract"]).To(gomega.Equal("implementation_result"), fmt.Sprintf("bad completion_status event: %v", event))

	gomega.Expect(event["blocker_kind"]).To(gomega.Equal("required_edit_paths_remaining"), fmt.Sprintf("missing blocker metadata: %v", event))
	gomega.Expect(event["edit_attempted"]).To(gomega.Equal(false), fmt.Sprintf("missing blocker metadata: %v", event))

	gomega.Expect(event["session_id"]).To(gomega.Equal("sess-1"), fmt.Sprintf("session_id missing on completion_status event: %v", event))
})

var _ = ginkgo.It("log run event emits implementation lifecycle state json", func() {
	t := ginkgo.GinkgoT()
	var buf bytes.Buffer

	model := &Model{}
	model.SetJSONEventStream(&buf, "sess-1")
	session := newDetachedPromptExecutionSession(context.Background(), model)

	session.logRunEvent(agent.RunEvent{
		Role:    "implementation_lifecycle_state",
		Content: `{"phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["internal/runtime/agent/runner_core.go"],"write_ready_nudge_count":2}`,
	})

	events := decodeJSONEvents(t, buf.String())
	gomega.Expect(len(events)).To(gomega.Equal(1), fmt.Sprintf("expected 1 event, got %d (%v)", len(events), events))

	event := events[0]
	gomega.Expect(event["type"]).To(gomega.Equal("implementation_lifecycle_state"), fmt.Sprintf("bad lifecycle event: %v", event))
	gomega.Expect(event["phase"]).To(gomega.Equal("implementation.target_write_ready"), fmt.Sprintf("bad lifecycle event: %v", event))

	gomega.Expect(event["stop_reason"]).To(gomega.Equal("model_call_budget_exhausted"), fmt.Sprintf("missing lifecycle metadata: %v", event))
	gomega.Expect(event["write_ready_nudge_count"]).To(gomega.Equal(float64(2)), fmt.Sprintf("missing lifecycle metadata: %v", event))

	gomega.Expect(event["session_id"]).To(gomega.Equal("sess-1"), fmt.Sprintf("session_id missing on lifecycle event: %v", event))
})

var _ = ginkgo.It("log run event stores internal recovery as run event", func() {
	t := ginkgo.GinkgoT()
	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:  t.TempDir(),
		Mode: runprofile.DebugLogModeFull,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewDebugLogger: %v", err))
	defer logger.Close()

	model := &Model{debugLogger: logger}
	session := newDetachedPromptExecutionSession(context.Background(), model)
	session.logRunEvent(agent.RunEvent{
		Role:    "completion_gate_recovery",
		Content: "internal recovery prompt",
	})

	body, err := os.ReadFile(logger.Path())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read debug log: %v", err))

	gomega.Expect(strings.Contains(string(body), `"type":"message"`)).To(gomega.BeFalse(), fmt.Sprintf("internal recovery event must not be written as a message record:\n%s", body))

	gomega.Expect(string(body)).To(gomega.ContainSubstring(`"type":"run_event"`), fmt.Sprintf("expected run_event record, got:\n%s", body))
	gomega.Expect(string(body)).To(gomega.ContainSubstring(`"role":"completion_gate_recovery"`), fmt.Sprintf("expected run_event record, got:\n%s", body))
})

var _ = ginkgo.It("log run event stores control rails as run event", func() {
	t := ginkgo.GinkgoT()
	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:  t.TempDir(),
		Mode: runprofile.DebugLogModeFull,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewDebugLogger: %v", err))
	defer logger.Close()

	model := &Model{debugLogger: logger}
	session := newDetachedPromptExecutionSession(context.Background(), model)
	session.logRunEvent(agent.RunEvent{
		Role:    "control_rails",
		Content: `{"allowed_tools":["read_file"]}`,
	})

	body, err := os.ReadFile(logger.Path())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read debug log: %v", err))

	gomega.Expect(strings.Contains(string(body), `"type":"message"`)).To(gomega.BeFalse(), fmt.Sprintf("control_rails must not be written as a message record:\n%s", body))

	gomega.Expect(string(body)).To(gomega.ContainSubstring(`"type":"run_event"`), fmt.Sprintf("expected control_rails run_event record, got:\n%s", body))
	gomega.Expect(string(body)).To(gomega.ContainSubstring(`"role":"control_rails"`), fmt.Sprintf("expected control_rails run_event record, got:\n%s", body))
})

var _ = ginkgo.It("json message skips control rails", func() {
	var buf bytes.Buffer

	model := &Model{}
	model.SetJSONEventStream(&buf, "sess-1")
	session := newDetachedPromptExecutionSession(context.Background(), model)
	session.eventStream.emitMessage("control_rails", `{"allowed_tools":["read_file"]}`)

	gomega.Expect(strings.TrimSpace(buf.String())).To(gomega.Equal(""), fmt.Sprintf("control_rails must not be emitted as a JSON message event:\n%s", buf.String()))
})

var _ = ginkgo.It("set json event stream nil disables", func() {
	t := ginkgo.GinkgoT()
	var buf bytes.Buffer

	model := &Model{}
	model.SetJSONEventStream(&buf, "")
	enabled := newDetachedPromptExecutionSession(context.Background(), model)
	enabled.emitJSONFinal("a")
	model.SetJSONEventStream(nil, "")
	disabled := newDetachedPromptExecutionSession(context.Background(), model)
	disabled.emitJSONFinal("b")

	events := decodeJSONEvents(t, buf.String())
	gomega.Expect(len(events)).To(gomega.Equal(1), fmt.Sprintf("expected 1 event after nil disable, got %d", len(events)))

	gomega.Expect(events[0]["content"]).To(gomega.Equal("a"), fmt.Sprintf("expected only first event, got %v", events[0]))
})
