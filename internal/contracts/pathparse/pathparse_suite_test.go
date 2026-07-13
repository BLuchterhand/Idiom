package pathparse

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPathParse(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Path Parsing Contracts Suite")
}
