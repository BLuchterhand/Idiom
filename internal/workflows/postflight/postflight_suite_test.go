package postflight

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPostflight(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Postflight Suite")
}
