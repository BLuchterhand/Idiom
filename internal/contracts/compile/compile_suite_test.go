package contractcompile

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestContractCompile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Contract Compile Suite")
}
