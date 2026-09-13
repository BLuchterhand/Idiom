package agent

import (
	"testing"

	"idiom/internal/platform/gotestenv"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func TestAgent(t *testing.T) {
	gotestenv.UseToolchain(t)
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Runtime Agent Suite")
}
