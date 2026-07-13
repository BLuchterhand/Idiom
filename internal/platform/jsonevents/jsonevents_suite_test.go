package jsonevents

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJSONEvents(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "JSON Events Suite")
}
