package queueadmission

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQueueAdmission(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Queue Admission Suite")
}
