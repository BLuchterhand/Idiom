package eval

import (
	"encoding/json"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("reports required model request findings", func() {
	records := []Record{{
		Type: "idiom.scripted_model.requests",
		Data: map[string]any{
			"request_facts": []any{
				map[string]any{"index": float64(0), "kind": "tool_capable", "transport": "textual", "effective_tool_names": []any{"edit_file", "source_sanity"}},
				map[string]any{"index": float64(1), "kind": "tool_capable", "transport": "textual", "effective_tool_names": []any{"source_sanity"}},
			},
		},
	}}

	gomega.Expect(requiredModelRequestFindings(records, []ModelRequestExpectation{
		{Index: 0, Transport: "textual", RequireTools: []string{"edit_file"}},
		{Index: 1, RequireTools: []string{"source_sanity"}, ForbidTools: []string{"edit_file"}},
	})).To(gomega.BeEmpty())

	findings := requiredModelRequestFindings(records, []ModelRequestExpectation{{Index: 1, RequireTools: []string{"edit_file"}}})
	gomega.Expect(strings.Join(findings, "\n")).To(gomega.ContainSubstring("did not expose required tool"))
})

var _ = ginkgo.It("round trips model request expectations through JSON", func() {
	scenario := Scenario{RequireModelRequests: []ModelRequestExpectation{{
		Index: 3, Transport: "textual", RequireTools: []string{"source_sanity"}, ForbidTools: []string{"edit_file"},
	}}}
	data, err := json.Marshal(scenario)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var decoded Scenario
	gomega.Expect(json.Unmarshal(data, &decoded)).To(gomega.Succeed())
	gomega.Expect(decoded.RequireModelRequests).To(gomega.Equal(scenario.RequireModelRequests))
})
