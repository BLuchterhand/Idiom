package app

import (
	"bytes"
	"encoding/json"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("json event stream adds schema version", func() {
	var out bytes.Buffer
	stream := newJSONEventStream(&out, "session-1")
	stream.emit(map[string]any{"type": "final", "content": "done"})

	var event map[string]any
	{
		err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &event)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode event: %v", err))
	}
	gomega.Expect(event["schema_version"]).To(gomega.Equal(JSONEventSchemaVersion), fmt.Sprintf("schema_version = %v, want %s", event["schema_version"], JSONEventSchemaVersion))
	gomega.Expect(event["session_id"]).To(gomega.Equal("session-1"), fmt.Sprintf("session_id = %v", event["session_id"]))
})
