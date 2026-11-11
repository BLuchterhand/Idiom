package cpp

import (
	"context"
	"path/filepath"
	"strings"

	"idiom/internal/repository/semantic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("C++ exact symbol resolution", func() {
	var index *CppIndex

	BeforeEach(func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "src", "a.hpp"), strings.TrimSpace(`
class Widget {
public:
    Widget();
    void offer();
};
class Gadget {
public:
    void offer();
};
void free_helper();
`)+"\n")
		mustWriteFile(filepath.Join(root, "src", "a.cpp"), strings.TrimSpace(`
#include "a.hpp"
Widget::Widget() {}
void Widget::offer() {
    free_helper();
}
void Gadget::offer() {
    free_helper();
}
void free_helper() {}
void free_helper_extra() {
    free_helper();
}
`)+"\n")

		var err error
		index, err = BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())
	})

	It("collapses a class and its constructors into one owner", func() {
		resolution := index.ResolveSymbol("Widget", "")
		Expect(resolution.Status).To(Equal(ResolutionResolved))
		Expect(resolution.Owner).To(Equal("Widget"))
	})

	It("fails closed when an unqualified name has several owners", func() {
		resolution := index.ResolveSymbol("offer", "")
		Expect(resolution.Status).To(Equal(ResolutionAmbiguous))
		Expect(resolution.Candidates).To(HaveLen(2))
	})

	It("resolves a qualified name to one owner", func() {
		resolution := index.ResolveSymbol("Gadget::offer", "")
		Expect(resolution.Status).To(Equal(ResolutionResolved))
		Expect(resolution.Owner).To(Equal("Gadget::offer"))
	})

	It("does not fuzzy match: a partial name is not found and offers candidates", func() {
		resolution := index.ResolveSymbol("free_help", "")
		Expect(resolution.Status).To(Equal(ResolutionNotFound))
		Expect(resolution.Candidates).NotTo(BeEmpty())
	})

	It("counts only exact-name callers and reports truncation", func() {
		calls, total := index.CallsIntoName("free_helper", 1)
		Expect(total).To(Equal(3))
		Expect(calls).To(HaveLen(1))

		all, _ := index.CallsIntoName("free_helper", 0)
		for _, call := range all {
			Expect(call.CalleeName).To(Equal("free_helper"))
		}
	})

	It("reports the phase when the context budget is exhausted", func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "x.cpp"), "int x() { return 1; }\n")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := BuildCppIndexWithOptions(root, semantic.IndexOptions{Context: ctx})

		var budget *BudgetError
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(budget))
	})
})
