package routeprobe

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRouteProbe(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Route Probe Suite")
}
