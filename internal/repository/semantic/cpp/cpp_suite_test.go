package cpp

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "C++ Semantic Index Suite")
}
