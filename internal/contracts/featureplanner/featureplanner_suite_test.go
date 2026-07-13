package featureplanner

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFeaturePlanner(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Feature Planner Contracts Suite")
}
