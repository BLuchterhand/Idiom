package rust

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRust(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Rust Semantic Index Suite")
}
