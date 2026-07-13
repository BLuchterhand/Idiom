package python

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPython(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Python Semantic Index Suite")
}
