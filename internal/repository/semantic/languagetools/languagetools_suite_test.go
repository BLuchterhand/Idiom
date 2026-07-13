package languagetools

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLanguageTools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Language Tools Suite")
}
