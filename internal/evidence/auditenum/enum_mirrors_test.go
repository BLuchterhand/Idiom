package auditenum

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Enum mirror audits", func() {
	ginkgo.It("lists source references and high-signal mirror patterns", func() {
		root := makeEnumMirrorFixture()
		stdout, stderr, code := runAudit("--enum", "AnimalType", "--root", root)

		gomega.Expect(code).To(gomega.Equal(0), stderr)
		gomega.Expect(stdout).To(gomega.And(
			gomega.ContainSubstring("diagnostic_logger.cpp"),
			gomega.ContainSubstring("behavior.cpp"),
			gomega.ContainSubstring("enums.h"),
			gomega.ContainSubstring("Step 1"),
			gomega.ContainSubstring("Step 2"),
			gomega.ContainSubstring("Step 3"),
		))
		gomega.Expect(stdout).NotTo(gomega.ContainSubstring("notes.txt"))
		gomega.Expect(stdout).NotTo(gomega.ContainSubstring("node_modules"))
		gomega.Expect(stdout).To(gomega.Or(
			gomega.ContainSubstring("static_assert"),
			gomega.ContainSubstring("case AnimalType::"),
		))
	})

	ginkgo.It("rejects a missing enum flag", func() {
		_, stderr, code := runAudit("--root", ginkgo.GinkgoT().TempDir())
		gomega.Expect(code).To(gomega.Equal(2))
		gomega.Expect(stderr).To(gomega.ContainSubstring("--enum is required"))
	})

	ginkgo.It("reports when the enum has no references", func() {
		stdout, _, code := runAudit("--enum", "NoSuchSymbol", "--root", ginkgo.GinkgoT().TempDir())
		gomega.Expect(code).To(gomega.Equal(1))
		gomega.Expect(stdout).To(gomega.ContainSubstring("no references found"))
	})

	ginkgo.It("matches the enum as a token rather than a substring", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAuditFile(root, "different.cpp", "MyEnumOther x; // unrelated symbol\n")
		writeAuditFile(root, "real.cpp", "MyEnum y;\n")

		stdout, stderr, code := runAudit("--enum", "MyEnum", "--root", root)
		gomega.Expect(code).To(gomega.Equal(0), stderr)
		gomega.Expect(stdout).To(gomega.ContainSubstring("real.cpp"))
		gomega.Expect(stdout).NotTo(gomega.ContainSubstring("different.cpp"))
	})

	ginkgo.It("uses the selected language profile for extensions and patterns", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAuditFile(root, "x.go", "State\nvar _State_name = \"running\"\n")
		writeAuditFile(root, "y.cpp", "State\n")

		stdout, stderr, code := runAudit("--enum", "State", "--language", "go", "--root", root)
		gomega.Expect(code).To(gomega.Equal(0), stderr)
		gomega.Expect(stdout).To(gomega.And(
			gomega.ContainSubstring("x.go"),
			gomega.ContainSubstring("_State_name"),
		))
		gomega.Expect(stdout).NotTo(gomega.ContainSubstring("y.cpp"))
	})

	ginkgo.It("rejects an unknown language profile", func() {
		_, stderr, code := runAudit(
			"--enum", "X",
			"--language", "klingon",
			"--root", ginkgo.GinkgoT().TempDir(),
		)
		gomega.Expect(code).To(gomega.Equal(2))
		gomega.Expect(stderr).To(gomega.ContainSubstring("unknown --language"))
	})

	ginkgo.It("distinguishes a missing search root from an empty result", func() {
		_, stderr, code := runAudit("--enum", "X", "--root", "/definitely-does-not-exist-xyz")
		gomega.Expect(code).To(gomega.Equal(1))
		gomega.Expect(stderr).To(gomega.ContainSubstring("search root"))
	})

	ginkgo.It("treats an extension override as replacing profile extensions", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAuditFile(root, "x.go", "Symbol\n")
		writeAuditFile(root, "y.cpp", "Symbol\n")

		stdout, stderr, code := runAudit("--enum", "Symbol", "--root", root, "--ext", "go")
		gomega.Expect(code).To(gomega.Equal(0), stderr)
		gomega.Expect(stdout).To(gomega.ContainSubstring("x.go"))
		gomega.Expect(stdout).NotTo(gomega.ContainSubstring("y.cpp"))
	})

	ginkgo.It("supports a patternless any-language file-list audit", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAuditFile(root, "src.go", "MySymbol used here\n")

		stdout, stderr, code := runAudit("--enum", "MySymbol", "--root", root, "--language", "any")
		gomega.Expect(code).To(gomega.Equal(0), stderr)
		gomega.Expect(stdout).To(gomega.And(
			gomega.ContainSubstring("Step 1"),
			gomega.ContainSubstring("Step 2"),
			gomega.ContainSubstring("Step 3"),
			gomega.ContainSubstring("src.go"),
			gomega.ContainSubstring("no high-signal mirror patterns"),
		))
	})

	ginkgo.It("exits successfully after rendering help", func() {
		_, _, code := runAudit("--help")
		gomega.Expect(code).To(gomega.Equal(0))
	})

	ginkgo.It("expands custom enum placeholders before matching", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAuditFile(root, "x.go", "var _StateName_0 = \"running\"\nState\n")

		stdout, stderr, code := runAudit(
			"--enum", "State",
			"--root", root,
			"--pattern", "_{ENUM}Name_",
			"--ext", "go",
		)
		gomega.Expect(code).To(gomega.Equal(0), stderr)
		gomega.Expect(stdout).To(gomega.ContainSubstring("_StateName_0"))
	})
})

func makeEnumMirrorFixture() string {
	root := ginkgo.GinkgoT().TempDir()
	files := map[string]string{
		"enums.h": "enum class AnimalType { Cat, Dog, COUNT };\n",
		"diagnostic_logger.cpp": `#include "enums.h"
const char* AnimalType_NAMES[] = {"cat", "dog"};
static_assert(sizeof(AnimalType_NAMES)/sizeof(*AnimalType_NAMES) == (int)AnimalType::COUNT, "name count drift");
`,
		"behavior.cpp": `#include "enums.h"
void handle(AnimalType t) {
    switch (t) {
        case AnimalType::Cat: break;
        case AnimalType::Dog: break;
    }
}
`,
		"notes.txt":             "AnimalType discussion goes here\n",
		"node_modules/index.js": "AnimalType\n",
	}
	for relative, body := range files {
		writeAuditFile(root, relative, body)
	}
	return root
}

func writeAuditFile(root, relative, body string) {
	path := filepath.Join(root, relative)
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
}

func runAudit(args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}
