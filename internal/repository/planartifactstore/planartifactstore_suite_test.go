package planartifactstore

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPlanArtifactStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Plan Artifact Store Suite")
}
