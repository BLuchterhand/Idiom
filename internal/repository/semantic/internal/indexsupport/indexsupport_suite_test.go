package indexsupport

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIndexSupport(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Semantic Index Support Suite")
}
