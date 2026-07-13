package lane

import (
	"bytes"
	"encoding/json"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
)

func setupPostflightProject(briefBody, diffBody string, files map[string]string) (root, briefPath, diffPath string) {

	root = ginkgo.GinkgoT().TempDir()
	briefPath = filepath.Join(root, "brief.md")
	gomega.Expect(os.WriteFile(briefPath, []byte(briefBody), 0o644)).To(gomega.Succeed())

	diffPath = filepath.Join(root, "lane.diff")
	gomega.Expect(os.WriteFile(diffPath, []byte(diffBody), 0o644)).To(gomega.Succeed())

	for relpath, body := range files {
		full := filepath.Join(root, relpath)
		gomega.Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(full, []byte(body), 0o644)).To(gomega.Succeed())

	}

	return root, briefPath, diffPath
}

const cleanDiff = `diff --git a/src/foo.go b/src/foo.go
--- a/src/foo.go
+++ b/src/foo.go
@@ -1,2 +1,3 @@
 package src
+// real fix
 func Foo() {}
`

const questionMarkerDiff = `diff --git a/src/foo.go b/src/foo.go
--- a/src/foo.go
+++ b/src/foo.go
@@ -1,2 +1,3 @@
 package src
+// <<QUESTION>>: did this resolve?
 func Foo() {}
`

const cleanBrief = `# Atom
Files To Modify
- src/foo.go
`

var _ = ginkgo.Describe("postflight", func() {
	ginkgo.It("lane postflight clean diff passes", func() {
		root, briefPath, diffPath := setupPostflightProject(cleanBrief, cleanDiff, map[string]string{
			"src/foo.go": "package src\n// real fix\nfunc Foo() {}\n",
		})

		var stdout, stderr bytes.Buffer

		code := runLaneMainTest([]string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root}, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("clean diff must exit 0, got %d (stderr=%s, stdout=%s)", code, stderr.String(), stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("PASS"), fmt.Sprintf("output should announce PASS, got: %s", stdout.String()))

	})
	ginkgo.It("lane postflight unresolved question marker fails exit three", func() {
		root, briefPath, diffPath := setupPostflightProject(cleanBrief, questionMarkerDiff, map[string]string{
			"src/foo.go": "package src\n// <<QUESTION>>: did this resolve?\nfunc Foo() {}\n",
		})

		var stdout, stderr bytes.Buffer

		code := runLaneMainTest([]string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root}, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(3), fmt.Sprintf("question marker must exit 3, got %d (stdout=%s)", code, stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("unresolved_question_marker"), fmt.Sprintf("output should name the check that failed: %s", stdout.String()))

	})
	ginkgo.It("lane postflight allowlist mismatch fails", func() {
		briefBody := `# Atom
Files To Modify
- src/foo.go
`
		diffBody := `diff --git a/src/wrong.go b/src/wrong.go
--- a/src/wrong.go
+++ b/src/wrong.go
@@ -1 +1,2 @@
 package src
+// out of scope
`
		root, briefPath, diffPath := setupPostflightProject(briefBody, diffBody, map[string]string{
			"src/wrong.go": "package src\n// out of scope\n",
		})

		var stdout, stderr bytes.Buffer

		code := runLaneMainTest([]string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root}, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(3), fmt.Sprintf("allowlist mismatch must exit 3, got %d (stdout=%s)", code, stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("file_allowlist_mismatch"), fmt.Sprintf("output should name the check: %s", stdout.String()))

	})
	ginkgo.It("lane postflight explicit required edit paths beats brief", func() {
		briefBody := `# Atom
Files To Modify
- src/should-not-match.go
`
		diffBody := `diff --git a/src/foo.go b/src/foo.go
--- a/src/foo.go
+++ b/src/foo.go
@@ -1 +1,2 @@
 package src
+// fix
`
		root, briefPath, diffPath := setupPostflightProject(briefBody, diffBody, map[string]string{
			"src/foo.go": "package src\n// fix\n",
		})

		var stdout, stderr bytes.Buffer

		args := []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--required-edit-paths", "src/foo.go", "--project-root", root}
		code := runLaneMainTest(args, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("explicit required_edit_paths must beat brief; got code=%d stdout=%s", code, stdout.String()))

	})
	ginkgo.It("lane postflight skip flag disables named check", func() {
		briefBody := cleanBrief
		diffBody := questionMarkerDiff
		root, briefPath, diffPath := setupPostflightProject(briefBody, diffBody, map[string]string{

			"src/foo.go": "package src\n// <<QUESTION>>: skip me\nfunc Foo() {}\n",
		})

		var stdout, stderr bytes.Buffer

		args := []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root, "--skip", "unresolved_question_marker"}
		code := runLaneMainTest(args, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("--skip must drop the named check; got code=%d stdout=%s", code, stdout.String()))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("unresolved_question_marker"), fmt.Sprintf("skipped check leaked into output: %s", stdout.String()))

	})
	ginkgo.It("lane postflight json emits findings and summary", func() {
		root, briefPath, diffPath := setupPostflightProject(cleanBrief, questionMarkerDiff, map[string]string{
			"src/foo.go": "package src\n// <<QUESTION>>: trigger\nfunc Foo() {}\n",
		})

		var stdout, stderr bytes.Buffer

		args := []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root, "--json"}
		code := runLaneMainTest(args, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(3), fmt.Sprintf("expected exit 3, got %d (stdout=%s)", code, stdout.String()))

		var (
			summary    map[string]any
			sawFinding bool
		)

		for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}

			var event map[string]any
			gomega.Expect(json.Unmarshal([]byte(line), &event)).To(gomega.Succeed())

			switch event["type"] {
			case "idiom.lane.postflight.finding":
				sawFinding = true
			case "idiom.lane.postflight.summary":
				summary = event
			}
		}
		gomega.Expect(sawFinding).To(gomega.BeTrue(), fmt.Sprintf("expected at least one finding event in JSON output: %s", stdout.String()))
		gomega.Expect(summary).NotTo(gomega.Equal(nil), fmt.Sprintf("expected a summary event: %s", stdout.String()))

		if pass, _ := summary["pass"].(bool); pass {
			ginkgo.Fail(fmt.Sprintf("summary.pass should be false when error findings exist: %v", summary))
		}
	})
	ginkgo.It("lane postflight missing diff flag exits two", func() {
		var stdout, stderr bytes.Buffer

		code := runLaneMainTest([]string{"postflight"}, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("missing --diff must exit 2, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--diff is required"), fmt.Sprintf("error must explain the missing flag: %s", stderr.String()))

	})
	ginkgo.It("lane postflight missing diff file exits one", func() {
		var stdout, stderr bytes.Buffer

		args := []string{"postflight", "--diff", "/does/not/exist.diff"}
		code := runLaneMainTest(args, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("missing diff file must exit 1 (IO), got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("read diff"), fmt.Sprintf("error must reference the diff read: %s", stderr.String()))

	})
	ginkgo.It("lane postflight project check loads and fails", func() {
		briefBody := cleanBrief
		diffBody := cleanDiff
		root, briefPath, diffPath := setupPostflightProject(briefBody, diffBody, map[string]string{
			"src/foo.go": "package src\n// real fix\nfunc Foo() {}\n",
		})
		dir := filepath.Join(root, ".idiom", "postflight", "checks")
		gomega.Expect(os.MkdirAll(dir, 0o755)).To(gomega.Succeed())

		rule := `{"name": "always-fail", "kind": "shell", "command": "false"}`
		gomega.Expect(os.WriteFile(filepath.Join(dir, "always-fail.json"), []byte(rule), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := runLaneMainTest([]string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root}, &stdout, &stderr, &laneFakeRunner{})
		gomega.Expect(code).To(gomega.Equal(3), fmt.Sprintf("project check failure must exit 3, got %d (stdout=%s)", code, stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("always-fail"), fmt.Sprintf("output should mention the project check that failed: %s", stdout.String()))

	})
})
