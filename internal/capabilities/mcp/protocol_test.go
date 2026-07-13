package mcp

import (
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("MCP resource protocol DTOs", func() {
	ginkgo.It("uses the MCP JSON shape for resource reads", func() {
		resource, err := json.Marshal(Resource{
			URI:         "idiom://workspace/root",
			Name:        "workspace root",
			Description: "Current workspace",
			MimeType:    "application/json",
			Meta:        json.RawMessage(`{"origin":"test"}`),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(resource)).To(gomega.Equal(`{"uri":"idiom://workspace/root","name":"workspace root","description":"Current workspace","mimeType":"application/json","_meta":{"origin":"test"}}`))

		params, err := json.Marshal(ResourceReadParams{URI: "idiom://workspace/root"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(params)).To(gomega.Equal(`{"uri":"idiom://workspace/root"}`))

		var result ResourceReadResult
		err = json.Unmarshal([]byte(`{"contents":[{"uri":"idiom://workspace/root","mimeType":"application/json","text":"{\"metadata_only\":true}"}]}`), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Contents).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("URI", "idiom://workspace/root"),
			gomega.HaveField("MimeType", "application/json"),
			gomega.HaveField("Text", `{"metadata_only":true}`),
		)))
	})
})
