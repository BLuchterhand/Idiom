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
		resolution := index.ResolveSymbol("free_helper", "")
		limited := index.CallsInto(resolution, "", 1)
		Expect(limited.Total).To(Equal(3))
		Expect(limited.Callers).To(HaveLen(1))

		all := index.CallsInto(resolution, "", 0)
		for _, call := range all.Callers {
			Expect(call.CalleeName).To(Equal("free_helper"))
		}
	})

	It("reports several calls on one line once", func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "d.cpp"), "void h() {}\nvoid g() { h(); h(); }\n")

		built, err := BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())

		calls := built.CallsInto(built.ResolveSymbol("h", ""), "", 0)
		Expect(calls.Callers).To(HaveLen(1))
		Expect(calls.Total).To(Equal(1))
	})

	It("applies the path filter before the cap and counts the filtered total", func() {
		resolution := index.ResolveSymbol("free_helper", "")
		calls := index.CallsInto(resolution, "src/a.cpp", 1)
		Expect(calls.Total).To(Equal(3))
		Expect(calls.Callers).To(HaveLen(1))

		none := index.CallsInto(resolution, "elsewhere/", 1)
		Expect(none.Callers).To(BeEmpty())
		Expect(none.Total).To(BeZero())
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

var _ = Describe("C++ in-class member attribution", func() {
	build := func(files map[string]string) *CppIndex {
		root := GinkgoT().TempDir()
		for rel, content := range files {
			mustWriteFile(filepath.Join(root, rel), strings.TrimSpace(content)+"\n")
		}

		index, err := BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())

		return index
	}

	It("keeps same-named inline members of different classes apart", func() {
		index := build(map[string]string{"a.hpp": `
class A {
public:
    void reset() { count = 0; }
};
class B {
public:
    void reset() { count = 1; }
};
`})
		Expect(index.ResolveSymbol("reset", "").Status).To(Equal(ResolutionAmbiguous))
		Expect(index.ResolveSymbol("A::reset", "")).To(HaveField("Owner", "A::reset"))
		Expect(index.ResolveSymbol("A::reset", "").Status).To(Equal(ResolutionResolved))
	})

	It("does not drop an inline member next to an out-of-class definition", func() {
		index := build(map[string]string{
			"a.hpp": `
class A {
public:
    void reset() { count = 0; }
};
class B {
public:
    void reset();
};
`,
			"b.cpp": `
#include "a.hpp"
void B::reset() { }
`})
		resolution := index.ResolveSymbol("reset", "")
		Expect(resolution.Status).To(Equal(ResolutionAmbiguous))
		Expect(resolution.Candidates).To(HaveLen(2))
	})

	It("keeps same-named types in different namespaces apart and folds their out-of-class members", func() {
		index := build(map[string]string{
			"p.hpp": `
namespace x {
class Pool {
public:
    void reset();
};
}
namespace y {
class Pool {
public:
    void reset();
};
}
`,
			"p.cpp": `
#include "p.hpp"
namespace x {
void Pool::reset() { }
}
`})
		Expect(index.ResolveSymbol("Pool", "").Status).To(Equal(ResolutionAmbiguous))
		Expect(index.ResolveSymbol("x::Pool", "").Status).To(Equal(ResolutionResolved))

		resolution := index.ResolveSymbol("x::Pool::reset", "")
		Expect(resolution.Status).To(Equal(ResolutionResolved))
		Expect(resolution.Owner).To(Equal("x::Pool::reset"))
		Expect(resolution.Definitions).To(HaveLen(2), "the in-class declaration and the out-of-class definition share one owner")
	})

	It("does not treat a call statement inside a function body as a definition", func() {
		index := build(map[string]string{
			"k.hpp": `
namespace ns {
inline void offer_frame(int a) {
    do_work(a);
}
}
`,
			"k.cpp": `
#include "k.hpp"
void run() {
    ns::offer_frame(
        1);
}
`})
		resolution := index.ResolveSymbol("ns::offer_frame", "")
		Expect(resolution.Status).To(Equal(ResolutionResolved))
		Expect(resolution.Definitions).To(HaveLen(1))
		Expect(resolution.Definitions[0].Path).To(Equal("k.hpp"))
	})
})
