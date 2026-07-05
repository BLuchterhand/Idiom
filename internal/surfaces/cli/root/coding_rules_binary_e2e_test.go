package root

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	rulescontract "idiom/internal/contracts/codingrules"
)

func TestCodingRulesBinaryE2ECoversPublicBehaviors(t *testing.T) {
	bin := buildIdiomBinaryForCodingRulesE2E(t)

	t.Run("coding rules advisory JSON passes", func(t *testing.T) {
		projectRoot := newCodingRulesE2EProject(t, "")

		result := runIdiomBinaryForCodingRulesE2E(t, bin,
			"coding-rules", "check",
			"--project-root", projectRoot,
			"--json",
		)

		if result.code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
		}

		var report rulescontract.Report
		if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
			t.Fatalf("decode report: %v\n%s", err, result.stdout)
		}
		if report.Status != rulescontract.StatusWarn || len(report.Findings) != 1 {
			t.Fatalf("unexpected report: %#v", report)
		}
		if strings.Contains(result.stdout, "return target") {
			t.Fatalf("report leaked raw source body: %s", result.stdout)
		}
	})

	t.Run("coding rules blocking exits three", func(t *testing.T) {
		projectRoot := newCodingRulesE2EProject(t, blockingCodingRulesPolicyE2E())

		result := runIdiomBinaryForCodingRulesE2E(t, bin,
			"coding-rules", "check",
			"--project-root", projectRoot,
			"--fail-on", "blocking",
		)

		if result.code != 3 {
			t.Fatalf("exit = %d, want 3\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
		}
		if !strings.Contains(result.stdout, "go.no_pass_through_wrapper") || !strings.Contains(result.stdout, "coding-rules: FAIL") {
			t.Fatalf("blocking output should name rule and fail summary:\n%s", result.stdout)
		}
	})

	t.Run("coding rules explicit missing config fails", func(t *testing.T) {
		projectRoot := newCodingRulesE2EProject(t, "")

		result := runIdiomBinaryForCodingRulesE2E(t, bin,
			"coding-rules", "check",
			"--project-root", projectRoot,
			"--config", filepath.Join(projectRoot, "missing.json"),
			"--json",
		)

		if result.code != 1 {
			t.Fatalf("exit = %d, want 1\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
		}
		if !strings.Contains(result.stderr, "does not exist") {
			t.Fatalf("stderr should explain missing config: %s", result.stderr)
		}
	})

	t.Run("coding rules path outside project root fails closed", func(t *testing.T) {
		projectRoot := newCodingRulesE2EProject(t, "")
		outsideRoot := t.TempDir()
		outsidePath := filepath.Join(outsideRoot, "outside.go")
		writeCodingRulesE2EFile(t, outsideRoot, "outside.go", `package outside

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`)

		result := runIdiomBinaryForCodingRulesE2E(t, bin,
			"coding-rules", "check",
			"--project-root", projectRoot,
			"--path", outsidePath,
			"--json",
		)

		if result.code != 1 {
			t.Fatalf("exit = %d, want 1\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
		}
		if !strings.Contains(result.stderr, "escapes project root") {
			t.Fatalf("stderr should explain project-root escape: %s", result.stderr)
		}
		if strings.Contains(result.stdout+result.stderr, "return target") {
			t.Fatalf("outside source body leaked in output:\nstdout=%s\nstderr=%s", result.stdout, result.stderr)
		}
	})

	t.Run("workspace tool returns coding rules payload", func(t *testing.T) {
		projectRoot := newCodingRulesE2EProject(t, blockingCodingRulesPolicyE2E())
		configPath := writeCodingRulesE2EConfig(t, projectRoot)

		result := runIdiomBinaryForCodingRulesE2E(t, bin,
			"tool", "coding_rules_check",
			"--config", configPath,
			"--input", `{"path":"."}`,
		)

		if result.code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
		}
		for _, want := range []string{
			`"Tool": "coding_rules_check"`,
			`"payload_type": "application/vnd.idiom.coding_rules.report+json"`,
			`"rule_id": "go.no_pass_through_wrapper"`,
			`"status": "fail"`,
		} {
			if !strings.Contains(result.stdout, want) {
				t.Fatalf("workspace tool output missing %q:\n%s", want, result.stdout)
			}
		}
		if strings.Contains(result.stdout, "return target") {
			t.Fatalf("workspace tool report leaked raw source body: %s", result.stdout)
		}
	})

	t.Run("lane postflight blocking coding rules fails", func(t *testing.T) {
		projectRoot := newCodingRulesE2EProject(t, blockingCodingRulesPolicyE2E())
		diffPath := writeCodingRulesE2EDiff(t, projectRoot)

		result := runIdiomBinaryForCodingRulesE2E(t, bin,
			"lane", "postflight",
			"--project-root", projectRoot,
			"--diff", diffPath,
		)

		if result.code != 3 {
			t.Fatalf("exit = %d, want 3\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
		}
		if !strings.Contains(result.stdout, "coding_rules") || !strings.Contains(result.stdout, "go.no_pass_through_wrapper") {
			t.Fatalf("postflight output should include coding rules failure:\n%s", result.stdout)
		}
	})

	t.Run("lane postflight advisory error does not fail", func(t *testing.T) {
		projectRoot := newCodingRulesE2EProject(t, advisoryErrorCodingRulesPolicyE2E())
		diffPath := writeCodingRulesE2EDiff(t, projectRoot)

		result := runIdiomBinaryForCodingRulesE2E(t, bin,
			"lane", "postflight",
			"--project-root", projectRoot,
			"--diff", diffPath,
		)

		if result.code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
		}
		if !strings.Contains(result.stdout, "coding_rules") || !strings.Contains(result.stdout, "[warning]") {
			t.Fatalf("postflight output should include advisory coding rules warning:\n%s", result.stdout)
		}
	})
}

type codingRulesE2EResult struct {
	code   int
	stdout string
	stderr string
}

func buildIdiomBinaryForCodingRulesE2E(t *testing.T) string {
	t.Helper()

	repoRoot := rootTestRepoRoot(t)
	bin := filepath.Join(t.TempDir(), "idiom")

	cmd := exec.Command("go", "build", "-o", bin, "./cmd/idiom")
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build idiom binary: %v\n%s", err, string(output))
	}

	return bin
}

func runIdiomBinaryForCodingRulesE2E(t *testing.T, bin string, args ...string) codingRulesE2EResult {
	t.Helper()

	cmd := exec.Command(bin, args...)
	cmd.Dir = rootTestRepoRoot(t)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())

	output, err := cmd.Output()
	result := codingRulesE2EResult{stdout: string(output)}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.code = exitErr.ExitCode()
			result.stderr = string(exitErr.Stderr)
			return result
		}

		t.Fatalf("run idiom %s: %v", strings.Join(args, " "), err)
	}

	return result
}

func newCodingRulesE2EProject(t *testing.T, policy string) string {
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

func writeCodingRulesE2EConfig(t *testing.T, projectRoot string) string {
	t.Helper()

	configPath := filepath.Join(projectRoot, "config.json")
	body := `{"workspace":{"root":` + quoteJSONForCodingRulesE2E(t, projectRoot) + `,"cwd":` + quoteJSONForCodingRulesE2E(t, projectRoot) + `}}`
	if err := os.WriteFile(configPath, []byte(body+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return configPath
}

func writeCodingRulesE2EDiff(t *testing.T, projectRoot string) string {
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
	if err := os.WriteFile(diffPath, []byte(diff), 0o644); err != nil {
		t.Fatalf("write diff: %v", err)
	}

	return diffPath
}

func writeCodingRulesE2EFile(t *testing.T, root string, rel string, body string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func quoteJSONForCodingRulesE2E(t *testing.T, value string) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("quote JSON: %v", err)
	}

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
