package compaction

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCompaction(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Compaction Provenance Suite")
}
