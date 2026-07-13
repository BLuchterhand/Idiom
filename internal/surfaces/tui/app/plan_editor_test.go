package app

import (
	"fmt"
	"reflect"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("split editor command parses quoted args without shell", func() {
	args, err := splitEditorCommand(`code --wait "plan file.md"`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("splitEditorCommand: %v", err))

	want := []string{"code", "--wait", "plan file.md"}
	gomega.Expect(reflect.DeepEqual(args, want)).To(gomega.BeTrue(), fmt.Sprintf("args = %#v, want %#v", args, want))
})

var _ = ginkgo.It("split editor command treats shell metacharacters as args", func() {
	args, err := splitEditorCommand(`vi; touch injected`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("splitEditorCommand: %v", err))

	want := []string{"vi;", "touch", "injected"}
	gomega.Expect(reflect.DeepEqual(args, want)).To(gomega.BeTrue(), fmt.Sprintf("args = %#v, want %#v", args, want))
})

var _ = ginkgo.It("split editor command rejects unterminated quote", func() {
	{
		_, err := splitEditorCommand(`code "unterminated`)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unterminated quote error"))
	}
})
