package language

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLanguage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Language Registry Suite")
}
