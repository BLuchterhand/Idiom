package jsoneventstest

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJSONEventsTestHelpers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "JSON Events Test Helpers Suite")
}
