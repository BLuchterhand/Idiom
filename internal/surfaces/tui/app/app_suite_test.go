package app

//go:generate go tool mockgen -destination=mock_system_search_access_approver_test.go -package=app -mock_names=SystemSearchAccessApprover=MockSystemSearchAccessApprover -typed idiom/internal/capabilities/workspace SystemSearchAccessApprover

import (
	"testing"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

func TestApp(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "TUI App Suite")
}
