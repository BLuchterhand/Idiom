package artifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("artifact", func() {
	ginkgo.It("artifact command writes output and json", func() {
		configPath := writeMainConfig()
		outputPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "artifact.md")
		jsonPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "events.jsonl")
		reportPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "artifact.report.json")

		var (
			stdout, stderr bytes.Buffer
			gotOpts        RunOptions
		)

		code := RunMainWithTelemetry(
			[]string{
				"-config", configPath,
				"-prompt", "draft a spec",
				"-output", outputPath,
				"-json-out", jsonPath,
				"-report-out", reportPath,
			},
			&stdout,
			&stderr,
			func(_ config.Config, prompt string, opts RunOptions, w io.Writer) (string, error) {
				gomega.Expect(prompt).To(gomega.Equal("draft a spec"), fmt.Sprintf("prompt = %q", prompt))

				gotOpts = opts
				_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"# Artifact\"}\n"))

				return "# Artifact", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(gotOpts.JSONEvents).To(gomega.BeTrue(), "artifact command must request JSON events from the prompt runner")
		gomega.Expect(stdout.String()).To(gomega.Equal(""), fmt.Sprintf("output file mode should not print artifact to stdout, got %q", stdout.String()))

		gomega.Expect(readTestFile(outputPath)).To(gomega.Equal("# Artifact"))
		gomega.Expect(readTestFile(jsonPath)).To(gomega.ContainSubstring(`"type":"final"`))

		report := readArtifactReport(reportPath)
		gomega.Expect(report.DispatchReady).To(gomega.BeTrue(), fmt.Sprintf("artifact report should be dispatch-ready: %#v", report))
		gomega.Expect(report.RepairSource).To(gomega.Equal("raw_model"), fmt.Sprintf("artifact report missing provenance fields: %#v", report))
		gomega.Expect(report.AnswerHash).NotTo(gomega.BeEmpty(), fmt.Sprintf("artifact report missing provenance fields: %#v", report))
		gomega.Expect(report.PromptHash).NotTo(gomega.BeEmpty(), fmt.Sprintf("artifact report missing provenance fields: %#v", report))
		gomega.Expect(report.OutputPath).To(gomega.Equal(outputPath), fmt.Sprintf("artifact report paths = output %q json %q", report.OutputPath, report.JSONOut))
		gomega.Expect(report.JSONOut).To(gomega.Equal(jsonPath), fmt.Sprintf("artifact report paths = output %q json %q", report.OutputPath, report.JSONOut))

	})
	ginkgo.It("artifact command forwards max token override", func() {
		configPath := writeMainConfig()

		var (
			stdout, stderr bytes.Buffer
			gotOpts        RunOptions
		)

		code := RunMainWithTelemetry(
			[]string{"-config", configPath, "-prompt", "draft", "-max-token", "4096"},
			&stdout,
			&stderr,
			func(_ config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
				gotOpts = opts
				return "artifact", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(gotOpts.JSONEvents).To(gomega.BeTrue(), "artifact command must request JSON events from the prompt runner")
		gomega.Expect(gotOpts.MaxTokensOverride).To(gomega.Equal(4096), fmt.Sprintf("MaxTokensOverride = %d, want 4096", gotOpts.MaxTokensOverride))

	})
	ginkgo.It("artifact command rejects negative max token", func() {
		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(
			[]string{"-prompt", "draft", "-max-token", "-1"},
			&stdout,
			&stderr,
			func(config.Config, string, RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("prompt runner should not be called")

				return "", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected max-token validation failure, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("non-negative"), fmt.Sprintf("expected max-token validation failure, code=%d stderr=%s", code, stderr.String()))

	})
	ginkgo.It("artifact command no tools wraps prompt and fails tool use", func() {
		configPath := writeMainConfig()
		reportPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "artifact.report.json")

		var (
			stdout, stderr bytes.Buffer
			gotPrompt      string
			gotOpts        RunOptions
		)

		code := RunMainWithTelemetry(
			[]string{"-config", configPath, "-prompt", "draft", "-no-tools", "-report-out", reportPath},
			&stdout,
			&stderr,
			func(_ config.Config, prompt string, opts RunOptions, w io.Writer) (string, error) {
				gotPrompt = prompt
				gotOpts = opts
				_, _ = w.Write([]byte("{\"type\":\"tool_request\",\"tool\":\"read_file\"}\n"))

				return "artifact", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected tool-use failure, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(gotPrompt).To(gomega.ContainSubstring("Do not call tools"), fmt.Sprintf("-no-tools did not wrap prompt: %q", gotPrompt))
		gomega.Expect(gotOpts.TaskFamily).To(gomega.Equal(workerruntime.ArtifactFinalAnswerTaskFamily), fmt.Sprintf("-no-tools TaskFamily = %q, want %s", gotOpts.TaskFamily, workerruntime.ArtifactFinalAnswerTaskFamily))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("read_file"), fmt.Sprintf("stderr should name offending tool, got %q", stderr.String()))

		report := readArtifactReport(reportPath)
		gomega.Expect(report.DispatchReady).To(gomega.BeFalse(), fmt.Sprintf("tool-use failure report = %#v", report))
		gomega.Expect(report.ToolUseDetected).To(gomega.BeTrue(), fmt.Sprintf("tool-use failure report = %#v", report))
		gomega.Expect(report.ToolName).To(gomega.Equal("read_file"), fmt.Sprintf("tool-use failure report = %#v", report))
		gomega.Expect(report.ArtifactContract).To(gomega.Equal("artifact.no_tools.v1"), fmt.Sprintf("artifact contract = %q", report.ArtifactContract))

	})
	ginkgo.It("artifact command mechanical repair unwraps outer fence", func() {
		configPath := writeMainConfig()
		outputPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "artifact.md")
		reportPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "artifact.report.json")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(
			[]string{
				"-config", configPath,
				"-prompt", "draft",
				"-output", outputPath,
				"-report-out", reportPath,
				"-mechanical-repair",
			},
			&stdout,
			&stderr,
			func(config.Config, string, RunOptions, io.Writer) (string, error) {
				return "```markdown\n# Artifact\n```", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected mechanical repair success, got %d stderr=%s", code, stderr.String()))

		gomega.Expect(readTestFile(outputPath)).To(gomega.Equal("# Artifact"))

		report := readArtifactReport(reportPath)
		gomega.Expect(report.RepairSource).To(gomega.Equal("mechanical"), fmt.Sprintf("expected mechanical repair provenance, got %#v", report))
		gomega.Expect(report.MechanicalRepairApplied).To(gomega.BeTrue(), fmt.Sprintf("expected mechanical repair provenance, got %#v", report))

	})
	ginkgo.It("artifact command runs validator with artifact env", func() {
		configPath := writeMainConfig()
		outputPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "artifact.md")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(
			[]string{
				"-config", configPath,
				"-prompt", "draft",
				"-output", outputPath,
				"-validator", "grep -q '^validated$' \"$IDIOM_ARTIFACT\"",
			},
			&stdout,
			&stderr,
			func(config.Config, string, RunOptions, io.Writer) (string, error) {
				return "validated", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected validator success, got %d stderr=%s", code, stderr.String()))

		gomega.Expect(readTestFile(outputPath)).To(gomega.Equal("validated"))
	})
	ginkgo.It("run artifact validator writes combined output to stderr", func() {
		artifactPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "artifact.md")
		gomega.Expect(os.WriteFile(artifactPath, []byte("artifact\n"), 0o644)).To(gomega.Succeed())

		var stderr bytes.Buffer
		exit, err := runArtifactValidator("printf stdout-marker; printf stderr-marker >&2", artifactPath, &stderr)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("validator failed: exit=%d err=%v stderr=%s", exit, err, stderr.String()))
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("exit = %d, want 0", exit))

		for _, want := range []string{"stdout-marker", "stderr-marker"} {
			gomega.Expect(stderr.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("stderr missing %q: %q", want, stderr.String()))

		}
	})
	ginkgo.It("run artifact validator returns failure exit code", func() {
		artifactPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "artifact.md")
		gomega.Expect(os.WriteFile(artifactPath, []byte("artifact\n"), 0o644)).To(gomega.Succeed())

		var stderr bytes.Buffer
		exit, err := runArtifactValidator("printf failing-validator >&2; exit 7", artifactPath, &stderr)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected validator failure"))
		gomega.Expect(exit).To(gomega.Equal(7), fmt.Sprintf("exit = %d, want 7; err=%v", exit, err))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("failing-validator"), fmt.Sprintf("stderr should include validator output, got %q", stderr.String()))

	})
	ginkgo.It("artifact command rejects prompt sources conflict", func() {
		configPath := writeMainConfig()
		promptPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "prompt.txt")
		gomega.Expect(os.WriteFile(promptPath, []byte("prompt\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(
			[]string{"-config", configPath, "-prompt", "x", "-prompt-file", promptPath},
			&stdout,
			&stderr,
			func(config.Config, string, RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("prompt runner should not be called")

				return "", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected prompt-source conflict, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("mutually exclusive"), fmt.Sprintf("expected prompt-source conflict, code=%d stderr=%s", code, stderr.String()))

	})
})

func writeMainConfig() string {

	root := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))

	path := filepath.Join(root, "config.json")
	gomega.Expect(os.WriteFile(path, []byte("{}\n"), 0o644)).To(gomega.Succeed())

	return path
}

func readArtifactReport(path string) artifactReport {

	var report artifactReport
	gomega.Expect(json.Unmarshal([]byte(readTestFile(path)), &report)).To(gomega.Succeed())

	return report
}

func readTestFile(path string) string {

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))

	return string(data)
}
