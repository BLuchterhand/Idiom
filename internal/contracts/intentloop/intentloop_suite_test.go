package intentloop

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIntentLoop(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Intent Loop Contracts Suite")
}
