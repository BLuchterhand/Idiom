package jsonevents

import (
	"bytes"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

var _ = Describe("JSON event emission", func() {
	It("keeps emitter-owned fields authoritative", func() {
		var stdout bytes.Buffer
		Expect(Emit(&stdout, true, "owned.event", map[string]any{
			"type": "caller.event",
			"ts":   "not-a-timestamp",
			"x":    float64(1),
		})).To(Succeed())

		var payload map[string]any
		Expect(json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &payload)).To(Succeed())
		Expect(payload).To(HaveKeyWithValue("type", "owned.event"))
		Expect(payload["ts"]).NotTo(Or(Equal("not-a-timestamp"), Equal("")))
		Expect(payload).To(HaveKeyWithValue("x", float64(1)))
	})

	It("returns marshal errors without writing partial output", func() {
		var stdout bytes.Buffer
		err := Emit(&stdout, true, "owned.event", map[string]any{"bad": func() {}})
		Expect(err).To(HaveOccurred())
		Expect(stdout.Bytes()).To(BeEmpty())
	})

	It("returns writer failures", func() {
		writerErr := errors.New("writer closed")
		Expect(Emit(failingWriter{err: writerErr}, true, "owned.event", nil)).To(MatchError(writerErr))
	})

	It("rejects a nil writer when emission is enabled", func() {
		Expect(Emit(nil, true, "owned.event", nil)).To(HaveOccurred())
	})

	It("rejects an empty event name when emission is enabled", func() {
		var stdout bytes.Buffer
		Expect(Emit(&stdout, true, " ", nil)).To(HaveOccurred())
	})

	It("does not validate inputs when emission is disabled", func() {
		Expect(Emit(nil, false, "", map[string]any{"bad": func() {}})).To(Succeed())
	})
})
