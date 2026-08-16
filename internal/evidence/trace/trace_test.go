package trace

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("redacted run traces", func() {
	It("records a causal model and tool graph without raw content", func() {
		recorder := NewRecorder("run-1")
		recorder.RecordRunEvent("model_call", `{"prompt":"secret"}`)
		requestID := recorder.RecordToolRequest("read_file", "request-1")
		recorder.RecordToolResult("request-1", StatusCompleted, map[string]any{"tool": "read_file", "source_path": "README.md"})

		document := recorder.Snapshot()
		Expect(requestID).NotTo(BeEmpty())
		Expect(Replay(document).Valid).To(BeTrue())
		data, err := recorder.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring("secret"))
		Expect(document.Spans).To(HaveLen(4))
	})

	It("rejects orphan spans and children of terminal spans", func() {
		document := Document{SchemaVersion: SchemaVersion, TraceID: "trace", Spans: []Span{
			{SpanID: "root", Kind: SpanRun, Status: StatusCompleted},
			{SpanID: "orphan", ParentSpanID: "missing", Kind: SpanEvidence, Status: StatusObserved},
			{SpanID: "child", ParentSpanID: "root", Kind: SpanVerification, Status: StatusObserved},
		}}

		report := Replay(document)
		Expect(report.Valid).To(BeFalse())
		Expect(report.Violations).To(ConsistOf("child of terminal span: child", "missing parent: orphan"))
	})

	It("rejects a child added below a terminal recorder span", func() {
		recorder := NewRecorder("run-2")
		root := recorder.Snapshot().Spans[0].SpanID
		evidence, err := recorder.Record(SpanEvidence, root, StatusCompleted, map[string]any{"path": "README.md"})
		Expect(err).NotTo(HaveOccurred())
		_, err = recorder.Record(SpanVerification, evidence, StatusObserved, nil)
		Expect(err).To(MatchError(ContainSubstring("terminal span")))
	})
})
