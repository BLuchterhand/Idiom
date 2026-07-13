package typescript

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTypescript(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "TypeScript Semantic Index Suite")
}
