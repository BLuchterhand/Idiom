package writerrouting

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWriterRouting(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Writer Routing Suite")
}
