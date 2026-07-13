package root

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	rulescontract "idiom/internal/contracts/codingrules"
)

var _ = ginkgo.Describe("Coding-rules binary", ginkgo.Ordered, ginkgo.Label("e2e", "smoke"), func() {
	var bin string

	ginkgo.BeforeAll(func() {
		bin = buildIdiomBinary(ginkgo.GinkgoT())
	})

	ginkgo.It("passes advisory findings and returns structured JSON", func() {
		t := ginkgo.GinkgoT()
		projectRoot := newCodingRulesE2EProject(t, "")

		result := runIdiomBinary(t, bin,
			"coding-rules", "check",
			"--project-root", projectRoot,
			"--json",
		)

		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", result.stdout, result.stderr)

		var report rulescontract.Report
		gomega.Expect(json.Unmarshal([]byte(result.stdout), &report)).To(gomega.Succeed(), result.stdout)
		gomega.Expect(report.Status).To(gomega.Equal(rulescontract.StatusWarn))
		gomega.Expect(report.Findings).To(gomega.HaveLen(1))
		gomega.Expect(result.stdout).NotTo(gomega.ContainSubstring("return target"))
	})

	ginkgo.It("exits three for blocking findings", func() {
		t := ginkgo.GinkgoT()
		projectRoot := newCodingRulesE2EProject(t, blockingCodingRulesPolicyE2E())

		result := runIdiomBinary(t, bin,
			"coding-rules", "check",
			"--project-root", projectRoot,
			"--fail-on", "blocking",
		)

		gomega.Expect(result.code).To(gomega.Equal(3), "stdout=%s\nstderr=%s", result.stdout, result.stderr)
		gomega.Expect(result.stdout).To(gomega.And(
			gomega.ContainSubstring("go.no_pass_through_wrapper"),
			gomega.ContainSubstring("coding-rules: FAIL"),
		))
	})

	ginkgo.It("fails when an explicit config is missing", func() {
		t := ginkgo.GinkgoT()
		projectRoot := newCodingRulesE2EProject(t, "")

		result := runIdiomBinary(t, bin,
			"coding-rules", "check",
			"--project-root", projectRoot,
			"--config", filepath.Join(projectRoot, "missing.json"),
			"--json",
		)

		gomega.Expect(result.code).To(gomega.Equal(1), "stdout=%s\nstderr=%s", result.stdout, result.stderr)
		gomega.Expect(result.stderr).To(gomega.ContainSubstring("does not exist"))
	})

	ginkgo.It("fails closed for a path outside the project root", func() {
		t := ginkgo.GinkgoT()
		projectRoot := newCodingRulesE2EProject(t, "")
		outsideRoot := t.TempDir()
		outsidePath := filepath.Join(outsideRoot, "outside.go")
		writeCodingRulesE2EFile(t, outsideRoot, "outside.go", `package outside

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`)

		result := runIdiomBinary(t, bin,
			"coding-rules", "check",
			"--project-root", projectRoot,
			"--path", outsidePath,
			"--json",
		)

		gomega.Expect(result.code).To(gomega.Equal(1), "stdout=%s\nstderr=%s", result.stdout, result.stderr)
		gomega.Expect(result.stderr).To(gomega.ContainSubstring("escapes project root"))
		gomega.Expect(result.stdout + result.stderr).NotTo(gomega.ContainSubstring("return target"))
	})

	ginkgo.It("returns a coding-rules payload through the workspace tool", func() {
		t := ginkgo.GinkgoT()
		projectRoot := newCodingRulesE2EProject(t, blockingCodingRulesPolicyE2E())
		configPath := writeCodingRulesE2EConfig(t, projectRoot)

		result := runIdiomBinary(t, bin,
			"tool", "coding_rules_check",
			"--config", configPath,
			"--input", `{"path":"."}`,
		)

		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", result.stdout, result.stderr)
		for _, want := range []string{
			`"Tool": "coding_rules_check"`,
			`"payload_type": "application/vnd.idiom.coding_rules.report+json"`,
			`"rule_id": "go.no_pass_through_wrapper"`,
			`"status": "fail"`,
		} {
			gomega.Expect(result.stdout).To(gomega.ContainSubstring(want))
		}
		gomega.Expect(result.stdout).NotTo(gomega.ContainSubstring("return target"))
	})

	ginkgo.It("fails lane postflight for blocking coding rules", func() {
		t := ginkgo.GinkgoT()
		projectRoot := newCodingRulesE2EProject(t, blockingCodingRulesPolicyE2E())
		diffPath := writeCodingRulesE2EDiff(t, projectRoot)

		result := runIdiomBinary(t, bin,
			"lane", "postflight",
			"--project-root", projectRoot,
			"--diff", diffPath,
		)

		gomega.Expect(result.code).To(gomega.Equal(3), "stdout=%s\nstderr=%s", result.stdout, result.stderr)
		gomega.Expect(result.stdout).To(gomega.And(
			gomega.ContainSubstring("coding_rules"),
			gomega.ContainSubstring("go.no_pass_through_wrapper"),
		))
	})

	ginkgo.It("keeps lane postflight successful for advisory findings", func() {
		t := ginkgo.GinkgoT()
		projectRoot := newCodingRulesE2EProject(t, advisoryErrorCodingRulesPolicyE2E())
		diffPath := writeCodingRulesE2EDiff(t, projectRoot)

		result := runIdiomBinary(t, bin,
			"lane", "postflight",
			"--project-root", projectRoot,
			"--diff", diffPath,
		)

		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", result.stdout, result.stderr)
		gomega.Expect(result.stdout).To(gomega.And(
			gomega.ContainSubstring("coding_rules"),
			gomega.ContainSubstring("[warning]"),
		))
	})
})

type binaryE2EResult struct {
	code   int
	stdout string
	stderr string
}

func buildIdiomBinary(t rootTestContext) string {
	t.Helper()

	repoRoot := rootTestRepoRoot(t)
	bin := filepath.Join(t.TempDir(), "idiom")

	cmd := exec.Command("go", "build", "-o", bin, "./cmd/idiom")
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "build idiom binary output:\n%s", string(output))

	return bin
}

func runIdiomBinary(t rootTestContext, bin string, args ...string) binaryE2EResult {
	t.Helper()

	cmd := exec.Command(bin, args...)
	cmd.Dir = rootTestRepoRoot(t)
	home := t.TempDir()
	cmd.Env = rootTestEnvironmentWithOverrides(os.Environ(), map[string]string{
		"HOME":                                 home,
		"IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK": "1",
		"IDIOM_TELEMETRY":                      "0",
	})
	gomega.Expect(cmd.Environ()).To(gomega.ContainElement("HOME="+home), "compiled child must see the isolated test home")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := binaryE2EResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.code = exitErr.ExitCode()
			return result
		}

		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "run idiom %s", strings.Join(args, " "))
	}

	return result
}

func newCodingRulesE2EProject(t rootTestContext, policy string) string {
	t.Helper()

	root := t.TempDir()
	writeCodingRulesE2EFile(t, root, "wrapper.go", `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`)

	if strings.TrimSpace(policy) != "" {
		writeCodingRulesE2EFile(t, root, ".idiom/coding_rules.json", policy)
	}

	return root
}

func writeCodingRulesE2EConfig(t rootTestContext, projectRoot string) string {
	t.Helper()

	configPath := filepath.Join(projectRoot, "config.json")
	body := `{"workspace":{"root":` + quoteJSONForCodingRulesE2E(t, projectRoot) + `,"cwd":` + quoteJSONForCodingRulesE2E(t, projectRoot) + `}}`
	gomega.Expect(os.WriteFile(configPath, []byte(body+"\n"), 0o644)).To(gomega.Succeed())

	return configPath
}

func writeCodingRulesE2EDiff(t rootTestContext, projectRoot string) string {
	t.Helper()

	diff := `diff --git a/wrapper.go b/wrapper.go
--- a/wrapper.go
+++ b/wrapper.go
@@ -1,5 +1,7 @@
 package sample
 
 func target(v string) string { return v }
 
+// added wrapper
 func Wrapper(v string) string {
 	return target(v)
 }
`
	diffPath := filepath.Join(projectRoot, "change.diff")
	gomega.Expect(os.WriteFile(diffPath, []byte(diff), 0o644)).To(gomega.Succeed())

	return diffPath
}

func writeCodingRulesE2EFile(t rootTestContext, root string, rel string, body string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed(), "mkdir %s", rel)
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed(), "write %s", rel)
}

func quoteJSONForCodingRulesE2E(t rootTestContext, value string) string {
	t.Helper()

	data, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return string(data)
}

func blockingCodingRulesPolicyE2E() string {
	return `{
  "schema_version": "idiom.coding_rules.v1",
  "rules": [{
    "id": "go.no_pass_through_wrapper",
    "enforcement": "blocking",
    "severity": "error"
  }]
}
`
}

func advisoryErrorCodingRulesPolicyE2E() string {
	return `{
  "schema_version": "idiom.coding_rules.v1",
  "rules": [{
    "id": "go.no_pass_through_wrapper",
    "enforcement": "advisory",
    "severity": "error"
  }]
}
`
}
