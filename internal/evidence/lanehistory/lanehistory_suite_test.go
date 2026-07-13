package lanehistory

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLaneHistory(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Lane History Suite")
}
