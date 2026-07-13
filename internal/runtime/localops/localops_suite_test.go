package localops

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLocalOps(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Local Operations Suite")
}
