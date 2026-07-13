package autonomy

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAutonomy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Autonomy Contracts Suite")
}
