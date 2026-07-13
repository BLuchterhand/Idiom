package sessionlog

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSessionLog(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Session Log Suite")
}
