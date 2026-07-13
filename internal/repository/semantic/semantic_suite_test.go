package semantic_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSemantic(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Semantic Suite")
}
