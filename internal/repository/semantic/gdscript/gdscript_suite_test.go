package gdscript_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGDScript(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GDScript Semantic Index Suite")
}
