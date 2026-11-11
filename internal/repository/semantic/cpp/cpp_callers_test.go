package cpp

import (
	"path/filepath"
	"regexp"
	"strconv"
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

var _ = Describe("C++ owner-resolved calls with same-named classes", func() {
	var index *CppIndex

	BeforeEach(func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "k.hpp"), strings.TrimSpace(`
namespace a {
class Kernel {
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
`)+"\n")
		mustWriteFile(filepath.Join(root, "k.cpp"), strings.TrimSpace(`
#include "k.hpp"
namespace b {
void inB(Kernel& k) {
    k.run(11);
    Kernel::run(12);
}
}
namespace a {
void inA(Kernel& k) {
    k.run(21);
}
}
void outside(Kernel& k) {
    k.run(31);
}
void lambdas(a::Kernel& m, godot::Thing& t) {
    auto run = [&](int n) { return n; };
    run(41);
    t.run(42);
}
`)+"\n")

		var err error

		index, err = BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())
	})

	lines := func(pattern string) (callers []int, unresolved map[int]string) {
		res := index.ResolveSymbol(pattern, "")
		Expect(res.Status).To(Equal(ResolutionResolved), pattern)

		found := index.CallsInto(res, "", 0)
		unresolved = map[int]string{}

		for _, call := range found.Callers {
			callers = append(callers, call.Line)
		}

		for _, call := range found.Unresolved {
			unresolved[call.Line] = call.Reason
		}

		return callers, unresolved
	}

	It("lets the enclosing namespace pick the class and reports the rest as ambiguous", func() {
		inB, unresolvedB := lines("b::Kernel::run")
		Expect(inB).To(ConsistOf(4, 5)) // k.run(11), Kernel::run(12) inside namespace b
		Expect(unresolvedB).To(HaveLen(1))
		Expect(unresolvedB[14]).To(ContainSubstring("matches several indexed classes"))

		inA, unresolvedA := lines("a::Kernel::run")
		Expect(inA).To(ConsistOf(10)) // k.run(21)
		Expect(unresolvedA).To(HaveLen(1))
		Expect(unresolvedA[14]).To(ContainSubstring("matches several indexed classes"))
	})

	It("does not read a local lambda call as a member call, and keeps external namespaces quiet", func() {
		_, unresolved := lines("a::Kernel::run")
		Expect(unresolved).NotTo(HaveKey(18), "run(41) is a local lambda")
		Expect(unresolved).NotTo(HaveKey(19), "godot:: has no indexed symbols")
	})

	It("marks a name aliased to different targets ambiguous", func() {
		aliases := map[string]string{}
		parseCppAliases("using Ref = a::Kernel;\n", aliases, nil)
		parseCppAliases("using Ref = b::Kernel;\n", aliases, nil)
		Expect(aliases["Ref"]).To(Equal(cppAmbiguousType))
	})
})

var _ = Describe("C++ owner-resolved calls through namespace aliases", func() {
	It("expands file and block namespace aliases and fails closed on unknown prefixes", func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "k.hpp"), strings.TrimSpace(`
namespace proj {
namespace sim {
class Kernel {
public:
    void run(int n);
};
}
}
`)+"\n")
		mustWriteFile(filepath.Join(root, "a.cpp"), strings.TrimSpace(`
#include "k.hpp"
namespace ks = proj::sim;
void viaReceiver(ks::Kernel& k) {
    k.run(11);
}
class Wrap : public ks::Kernel {
public:
    void go() { run(21); }
    void lam() {
        std::function<void(int)> run{[](int) {}};
        run(71);
    }
};
void viaQualifier() {
    ks::Kernel::run(31);
}
void blockScoped(int x) {
    namespace kb = proj::sim;
    kb::Kernel* p = nullptr;
    p->run(41);
}
void unknownPrefix(zz::Kernel& z, godot::Thing& t) {
    z.run(51);
    t.run(52);
    zz::Kernel::run(53);
}
`)+"\n")
		mustWriteFile(filepath.Join(root, "f.hpp"), "namespace proj {\nnamespace sim {\nvoid step(int n);\n}\n}\n")
		mustWriteFile(filepath.Join(root, "f.cpp"), "#include \"f.hpp\"\nnamespace fs = proj::sim;\nvoid callsStep() {\n    fs::step(81);\n}\n")

		index, err := BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())

		res := index.ResolveSymbol("proj::sim::Kernel::run", "")
		Expect(res.Status).To(Equal(ResolutionResolved))

		found := index.CallsInto(res, "", 0)
		callers := []int{}

		for _, call := range found.Callers {
			callers = append(callers, call.Line)
		}

		unresolved := map[int]string{}
		for _, call := range found.Unresolved {
			unresolved[call.Line] = call.Reason
		}

		// k.run(11) receiver, run(21) base, ks::Kernel::run(31) qualifier, p->run(41) block alias.
		Expect(callers).To(ConsistOf(4, 8, 15, 20))
		Expect(unresolved).To(HaveLen(2))
		Expect(unresolved[23]).To(ContainSubstring("`zz::Kernel` is not an indexed class"))
		Expect(unresolved[25]).To(ContainSubstring("`zz::Kernel` is not an indexed class"))
		Expect(unresolved).NotTo(HaveKey(24), "godot::Thing is no indexed class name")
		Expect(callers).NotTo(ContainElement(11), "run(71) is a brace-initialised local")

		step := index.CallsInto(index.ResolveSymbol("proj::sim::step", ""), "", 0)
		Expect(step.Total).To(Equal(1), "fs::step(81): a bare alias qualifier expands too")

		for _, file := range index.Files {
			for _, symbol := range file.Symbols {
				Expect(symbol.Kind == "namespace" && (symbol.Name == "ks" || symbol.Name == "kb")).To(BeFalse(), "an alias is not a namespace")
			}
		}
	})
})

// The property behind every owner-resolved query: a call with the owner's
// method name is never dropped unless the resolver proves it reaches another
// class. Every call form is crossed with every way of writing the type name.
var _ = Describe("C++ owner-resolved calls never drop a possible caller", func() {
	type nameCase struct {
		owner  string // the pattern queried
		name   string // how the type is written
		alias  string // a file-scoped namespace alias the name relies on
		caller bool   // true: must be a caller; false: must be unresolved
	}

	cases := []nameCase{
		{"a::Kernel::run", "a::Kernel", "", true},
		{"a::Kernel::run", "ka::Kernel", "namespace ka = a;", true},
		{"a::Kernel::run", "ha::Kernel", "", true}, // alias from the included header
		{"a::Kernel::run", "Kernel", "", false},    // a::Kernel or b::Kernel
		{"a::Kernel::run", "Mystery", "", false},   // not indexed
		{"a::Kernel::run", "zz::Kernel", "", false},
		{"proj::sim::Core::run", "proj::sim::Core", "", true},
		{"proj::sim::Core::run", "ks::Core", "namespace ks = proj::sim;", true},
		{"proj::sim::Core::run", "hs::Core", "", true},
		{"proj::sim::Core::run", "Mystery", "", false},
		{"proj::sim::Core::run", "zz::Core", "", false},
	}

	// Each form is a source snippet with @N for the type name and @D for a
	// class name unique to the case; every call line holds one run( call, and
	// a line marked // none must be proven not to reach the owner.
	forms := []string{
		"void @Dq() {\n    @N::run(1);\n}",
		"void @Dr(@N& v) {\n    v.run(2);\n}",
		"void @Dp(@N* v) {\n    v->run(3);\n}",
		"void @Ds(std::unique_ptr<@N> v) {\n    v->run(4);\n}",
		"class @D : public @N {\npublic:\n    void go() {\n        run(5);\n    }\n    void go2() {\n        this->run(6);\n    }\n};",
		"void @Dd(@D& v) {\n    v.run(7);\n}",
		"void @De() {\n    @D::run(8);\n}",
		// a using-declaration keeps the base member visible beside an overload
		"class @DU : public @N {\npublic:\n    using @N::run;\n    void run();\n};\nvoid @Du(@DU& v) {\n    v.run(9);\n}",
		"template <class T> class @DT : public @N {\npublic:\n    using @N::run;\n    void run(T x);\n};\nvoid @Dt(@DT<int>& v) {\n    v.run(10);\n}",
		// a local of the same name shadows only after its declaration, in its block
		// block comments: trailing on a call line, and spanning lines between methods
		"class @DC : public @N {\npublic:\n    void go() {\n        run(15); /* trailing */\n    }\n    /* a comment\n       void run(); */\n    void go2() {\n        run(16); /* one */ /* two\n        */\n    }\n};",
		"class @DL : public @N {\npublic:\n    void go() {\n        run(11);\n        auto run = [](int) {};\n        run(12); // none\n    }\n    void go2() {\n        { bool run = false; }\n        run(13);\n    }\n};",
	}

	It("reports every call through the owner, an ambiguous, unindexed or aliased name as caller or unresolved", func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "k.hpp"), strings.TrimSpace(`
namespace a {
class Kernel {
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
namespace proj {
namespace sim {
class Core {
public:
    void run(int n);
};
}
}
`)+"\n")
		mustWriteFile(filepath.Join(root, "h.hpp"), "namespace ha = a;\nnamespace hs = proj::sim;\n")

		// An out-of-class definition after `using namespace b` names W, which is
		// both a::W and b::W, so it cannot prove a::W hides a::Kernel::run.
		mustWriteFile(filepath.Join(root, "w.hpp"), strings.TrimSpace(`
#include "k.hpp"
namespace a {
class W : public Kernel {
};
}
namespace b {
class W : public Kernel {
public:
    void run(int n);
};
}
`)+"\n")
		mustWriteFile(filepath.Join(root, "w.cpp"), "#include \"w.hpp\"\nusing namespace b;\nvoid W::run(int n) {}\nvoid useW(a::W& v) {\n    v.run(14);\n}\n")

		// want[owner][path:line] is the required verdict: caller, unresolved or none.
		want := map[string]map[string]string{"a::Kernel::run": {"w.cpp:5": "unresolved"}}

		for ci, c := range cases {
			path := "case" + string(rune('a'+ci)) + ".cpp"
			derived := "D" + string(rune('a'+ci))
			body := []string{"#include \"k.hpp\"", "#include \"h.hpp\""}

			if c.alias != "" {
				body = append(body, c.alias)
			}

			for _, form := range forms {
				form = strings.ReplaceAll(form, "@N", c.name)
				form = strings.ReplaceAll(form, "@D", derived)
				body = append(body, strings.Split(form, "\n")...)
			}

			if want[c.owner] == nil {
				want[c.owner] = map[string]string{}
			}

			for i, line := range body {
				if !callLine.MatchString(line) {
					continue // declarations such as `void run(T x);`
				}

				switch {
				case strings.Contains(line, "// none"):
					want[c.owner][path+":"+itoa(i+1)] = "none"
				case c.caller:
					want[c.owner][path+":"+itoa(i+1)] = "caller"
				default:
					want[c.owner][path+":"+itoa(i+1)] = "unresolved"
				}
			}

			mustWriteFile(filepath.Join(root, path), strings.Join(body, "\n")+"\n")
		}

		index, err := BuildCppIndex(root)
		Expect(err).NotTo(HaveOccurred())

		for owner, expected := range want {
			res := index.ResolveSymbol(owner, "")
			Expect(res.Status).To(Equal(ResolutionResolved), owner)

			found := index.CallsInto(res, "", 0)
			got := map[string]string{}

			for _, call := range found.Callers {
				got[call.Caller.Path+":"+itoa(call.Line)] = "caller"
			}

			for _, call := range found.Unresolved {
				got[call.Caller.Path+":"+itoa(call.Line)] = "unresolved"
			}

			for site, verdict := range expected {
				if verdict == "none" {
					verdict = ""
				}

				Expect(got[site]).To(Equal(verdict), owner+" at "+site)
			}
		}
	})
})

var callLine = regexp.MustCompile(`run\(\d+\)`)

func itoa(n int) string {
	return strconv.Itoa(n)
}
