package root

import (
	"os"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type rootTestContext interface {
	Helper()
	Setenv(key, value string)
	TempDir() string
}

func TestRoot(t *testing.T) {
	if os.Getenv(signalExitHelperEnv) == "1" {
		runSignalExitHelperProcess(t)

		return
	}

	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "CLI Root Suite")
}
