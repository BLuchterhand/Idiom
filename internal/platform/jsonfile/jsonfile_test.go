package jsonfile

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("JSON files", func() {
	ginkgo.It("writes formatted JSON with a trailing newline and reads it back", func() {
		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "sub", "test.json")
		input := map[string]any{"key": "value", "num": 42}

		gomega.Expect(WriteJSON(path, input)).To(gomega.Succeed())
		data, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(data).NotTo(gomega.BeEmpty())
		gomega.Expect(data).To(gomega.HaveSuffix("\n"))

		var got map[string]any
		gomega.Expect(ReadJSON(path, &got)).To(gomega.Succeed())
		gomega.Expect(got).To(gomega.HaveKeyWithValue("key", "value"))
		gomega.Expect(got).To(gomega.HaveKeyWithValue("num", float64(42)))
	})

	ginkgo.It("rejects an empty file", func() {
		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "empty.json")
		gomega.Expect(os.WriteFile(path, nil, 0o644)).To(gomega.Succeed())

		var value any
		gomega.Expect(ReadJSON(path, &value)).NotTo(gomega.Succeed())
	})

	ginkgo.It("rejects values that cannot be marshaled", func() {
		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "invalid.json")
		gomega.Expect(WriteJSON(path, make(chan int))).NotTo(gomega.Succeed())
	})

	ginkgo.It("rejects invalid JSON", func() {
		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "invalid.json")
		gomega.Expect(os.WriteFile(path, []byte("not json"), 0o644)).To(gomega.Succeed())

		var value json.RawMessage
		gomega.Expect(ReadJSON(path, &value)).NotTo(gomega.Succeed())
	})
})
