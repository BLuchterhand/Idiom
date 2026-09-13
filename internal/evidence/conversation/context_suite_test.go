package conversation

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConversationContext(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Conversation context evidence suite")
}
