package llama

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLlama(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Llama Suite")
}
