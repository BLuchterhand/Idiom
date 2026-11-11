package cpp

import (
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const callersHeader = `
class Alpha {
public:
    void run(int n);
    static void make();
private:
    int count_ = 0;
};
class Beta {
public:
    void run(int n);
};
class Child : public Alpha {
public:
    void poke();
};
class Twin : public Alpha {
public:
    void run(int n);
};
class Holder {
public:
    void go();
private:
    Alpha* target_;
    std::unique_ptr<Alpha> owned_;
    Beta other_;
};
using AlphaRef = Alpha;
void run(int n);
`

// Every call line is unique so expectations can name the line by its text.
const callersSource = `
#include "types.hpp"
void Alpha::run(int n) {
    make();
    this->make();
    count_ += n;
}
void Alpha::make() {
    run(101);
    this->run(102);
}
void Beta::run(int n) {
    run(201);
}
void Child::poke() {
    run(301);
}
void Twin::run(int n) {
    run(401);
}
void Holder::go() {
    target_->run(501);
    owned_->run(502);
    other_.run(503);
}
void run(int n) {}
void direct(Alpha& a, Beta* b, const Child& c, Twin t, AlphaRef* r) {
    a.run(601);
    b->run(602);
    c.run(603);
    t.run(604);
    r->run(605);
    Alpha::make();
    Alpha local;
    local.run(606);
    run(607);
}
void unclear(Alpha& a) {
    auto x = pick();
    x.run(701);
    a.next().run(702);
    stranger.run(703);
    auto* y = pick();
    y->run(704);
}
`

var _ = Describe("C++ owner-resolved incoming calls", func() {
	var (
		index *CppIndex
		src   []string
	)

	BeforeEach(func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "types.hpp"), strings.TrimSpace(callersHeader)+"\n")
		mustWriteFile(filepath.Join(root, "types.cpp"), strings.TrimSpace(callersSource)+"\n")

		src = strings.Split(strings.TrimSpace(callersSource), "\n")

		var err error

		index, err = BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())
	})

	// arg names the numeric tag of the call on a source line, e.g. "601".
	arg := func(line int) string {
		text := src[line-1]
		open := strings.LastIndex(text, "(")

		return text[open+1 : strings.LastIndex(text, ")")]
	}

	callers := func(pattern string) ([]string, CppCallers) {
		res := index.ResolveSymbol(pattern, "")
		Expect(res.Status).To(Equal(ResolutionResolved), pattern)

		found := index.CallsInto(res, "", 0)
		tags := make([]string, 0, len(found.Callers))

		for _, call := range found.Callers {
			tags = append(tags, arg(call.Line))
		}

		return tags, found
	}

	It("keeps same-named members of other classes out of the callers", func() {
		tags, _ := callers("Alpha::run")
		// own members (101, 102), derived without override (301), field and
		// smart-pointer receivers (501, 502), reference, derived-by-ref and
		// alias-pointer params and a local (601, 603, 605, 606).
		Expect(tags).To(ConsistOf("101", "102", "301", "501", "502", "601", "603", "605", "606"))
	})

	It("matches Beta::run only through Beta receivers and its own members", func() {
		tags, _ := callers("Beta::run")
		Expect(tags).To(ConsistOf("201", "503", "602"))
	})

	It("stops at a derived class that redeclares the method", func() {
		tags, _ := callers("Twin::run")
		Expect(tags).To(ConsistOf("401", "604"))
	})

	It("counts qualified and unqualified static calls", func() {
		res := index.ResolveSymbol("Alpha::make", "")
		found := index.CallsInto(res, "", 0)
		Expect(found.Total).To(Equal(3), "make(), this->make() and Alpha::make()")
	})

	It("reports unreadable receivers as unresolved instead of callers", func() {
		_, found := callers("Alpha::run")
		reasons := map[string]string{}

		for _, call := range found.Unresolved {
			reasons[arg(call.Line)] = call.Reason
		}

		Expect(reasons).To(HaveLen(4))
		Expect(reasons["701"]).To(ContainSubstring("auto-typed"))
		Expect(reasons["702"]).To(ContainSubstring("call result"))
		Expect(reasons["703"]).To(ContainSubstring("unknown"))
		Expect(reasons["704"]).To(ContainSubstring("auto-typed"))
	})

	It("limits both lists but keeps their totals", func() {
		res := index.ResolveSymbol("Alpha::run", "")
		found := index.CallsInto(res, "", 2)
		Expect(found.Callers).To(HaveLen(2))
		Expect(found.Total).To(Equal(9))
		Expect(found.Unresolved).To(HaveLen(2))
		Expect(found.UnresolvedTotal).To(Equal(4))
	})

	It("resolves a free function by namespace qualifier", func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "f.cpp"), strings.TrimSpace(`
namespace one {
void tick(int n) {}
}
namespace two {
void tick(int n) {}
}
struct Box {
    void tick(int n);
};
void use(Box& b) {
    one::tick(1);
    two::tick(2);
    b.tick(3);
}
`)+"\n")

		built, err := BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())

		res := built.ResolveSymbol("one::tick", "")
		Expect(res.Status).To(Equal(ResolutionResolved))

		found := built.CallsInto(res, "", 0)
		Expect(found.Total).To(Equal(1))
		Expect(found.Callers[0].Line).To(Equal(11))
		Expect(found.UnresolvedTotal).To(BeZero())
	})
})

var _ = Describe("C++ owner-resolved calls fail closed", func() {
	var (
		index *CppIndex
		src   []string
	)

	const header = `
namespace a {
class Base {
public:
    virtual void run(int n);
};
class Kernel : public Base {
public:
    void run(int n);
};
}
namespace b {
class Kernel {
public:
    void run(int n);
};
}
template <class T> class Mix : public T {
public:
    void go();
};
`

	const source = `
#include "k.hpp"
void viaBase(a::Base* p) {
    p->run(21);
}
template <typename K> void drive(K& k) {
    k.run(31);
}
void andand(bool on, a::Kernel* k) {
    if (on && k) k->run(41);
}
void mult(int s, a::Kernel* q) {
    int z = s * q;
    q->run(51);
}
void other(b::Kernel& w) {
    w.run(61);
}
void unk(Unknown& u) {
    u.run(81);
}
void stdtype(std::vector<int>& v) {
    v.run(91);
}
void exprOnly(int s) {
    int z = s * q;
    q->run(101);
}
template <class T> void Mix<T>::go() {
    run(111);
}
void shadow(a::Kernel& x) {
    b::Kernel* x2 = nullptr;
    x.run(121);
}
`

	BeforeEach(func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "k.hpp"), strings.TrimSpace(header)+"\n")
		mustWriteFile(filepath.Join(root, "k.cpp"), strings.TrimSpace(source)+"\n")

		src = strings.Split(strings.TrimSpace(source), "\n")

		var err error

		index, err = BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())
	})

	tag := func(line int) string {
		text := src[line-1]

		return text[strings.LastIndex(text, "(")+1 : strings.LastIndex(text, ")")]
	}

	It("resolves a::Kernel::run and sorts every other receiver into callers or unresolved", func() {
		res := index.ResolveSymbol("a::Kernel::run", "")
		Expect(res.Status).To(Equal(ResolutionResolved))

		found := index.CallsInto(res, "", 0)
		callers := []string{}

		for _, call := range found.Callers {
			callers = append(callers, tag(call.Line))
		}

		// 41 and 51 stay readable despite `on && k` and `s * q`; 121 too.
		Expect(callers).To(ConsistOf("41", "51", "121"))

		reasons := map[string]string{}
		for _, call := range found.Unresolved {
			reasons[tag(call.Line)] = call.Reason
		}

		Expect(reasons).To(HaveLen(5))
		Expect(reasons["21"]).To(ContainSubstring("virtual dispatch via base"))
		Expect(reasons["31"]).To(ContainSubstring("not an indexed class"))
		Expect(reasons["81"]).To(ContainSubstring("not an indexed class"))
		Expect(reasons["101"]).To(ContainSubstring("unknown"))
		Expect(reasons["111"]).To(ContainSubstring("not an indexed class"), "CRTP caller class")
		Expect(reasons).NotTo(HaveKey("61"), "b::Kernel is a different indexed class")
		Expect(reasons).NotTo(HaveKey("91"), "std types cannot be the owner")
	})
})
