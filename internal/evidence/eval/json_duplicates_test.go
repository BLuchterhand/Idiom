package eval

import (
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("rejects duplicate nested SWE suite keys", func() {
	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "suite.json")
	data := `{
  "name": "duplicates",
  "tasks": [{
    "id": "task",
    "family": "implementation",
    "prompt": "edit",
    "scenario": {
      "name": "task",
      "require_model_requests": [{"index": 3}],
      "require_model_requests": [{"index": 0}]
    }
}]
}`
	gomega.Expect(os.WriteFile(path, []byte(data), 0o600)).To(gomega.Succeed())

	_, err := ReadSWESuite(path)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(`duplicate JSON object key "require_model_requests"`)))
})
