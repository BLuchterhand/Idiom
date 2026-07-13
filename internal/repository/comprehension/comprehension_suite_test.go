package comprehension

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestComprehension(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Comprehension Suite")
}
