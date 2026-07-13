package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
	"idiom/internal/surfaces/cli/runrecord"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
)

var _ = ginkgo.Describe("headless", func() {
	ginkgo.It("run applies policy overrides and writes completion", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)
		summaryPath := filepath.Join(root, "summary.json")

		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"--config", configPath,
			"--prompt", "summarize the repo",
			"--output", "json",
			"--session", "run-test-1",
			"--permission-profile", "read-only",
			"--deny-tool", "shell",
			"--allow-tool", "read_file",
			"--deny-path", "secrets/**",
			"--max-tool-steps", "3",
			"--timeout", "2s",
			"--summary-out", summaryPath,
		}, bytes.NewReader(nil), &stdout, &stderr, func(cfg config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gomega.Expect(prompt).To(gomega.Equal("summarize the repo"), fmt.Sprintf("prompt = %q", prompt))
			gomega.Expect(opts.JSONEvents).To(gomega.BeFalse(), "json output mode should not force JSONL event output")
			gomega.Expect(cfg.Workspace.EnableWrites).To(gomega.BeFalse(), "read-only permission profile should disable writes")
			gomega.Expect(cfg.Chat.MaxToolSteps).To(gomega.Equal(3), fmt.Sprintf("MaxToolSteps = %d, want 3", cfg.Chat.MaxToolSteps))
			gomega.Expect(cfg.Chat.RequestTimeoutSeconds).To(gomega.Equal(2), fmt.Sprintf("timeout projection = request %d start %d", cfg.Chat.RequestTimeoutSeconds, cfg.Chat.ServerStartTimeoutSecs))
			gomega.Expect(cfg.Chat.ServerStartTimeoutSecs).To(gomega.BeZero(), fmt.Sprintf("timeout projection = request %d start %d", cfg.Chat.RequestTimeoutSeconds, cfg.Chat.ServerStartTimeoutSecs))

			if got := cfg.Workspace.ToolRules["shell"][0].Decision; got != "deny" {
				ginkgo.Fail(fmt.Sprintf("shell first decision = %q", got))
			}
			if got := cfg.Workspace.ToolRules["read_file"][0].Decision; got != "allow" {
				ginkgo.Fail(fmt.Sprintf("read_file decision = %q", got))
			}
			if got := cfg.Workspace.ToolRules["edit_file"][0]; got.Pattern != "secrets/**" || got.Decision != "deny" {
				ginkgo.Fail(fmt.Sprintf("edit_file deny rule = %#v", got))
			}

			return "answer", nil
		}, nil, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("Run exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		var output runrecord.RunOutputRecord
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &output)).To(gomega.Succeed())

		record := output.RunCompletionRecord
		gomega.Expect(output.Answer).To(gomega.Equal("answer"), fmt.Sprintf("json output should include final answer, got %q", output.Answer))
		gomega.Expect(record.SchemaVersion).To(gomega.Equal(runrecord.RunCompletionSchemaVersion), fmt.Sprintf("unexpected completion identity: %#v", record))
		gomega.Expect(record.Type).To(gomega.Equal(runrecord.RunCompletionRecordType), fmt.Sprintf("unexpected completion identity: %#v", record))
		gomega.Expect(record.Artifacts["completion"]).NotTo(gomega.BeEmpty(), fmt.Sprintf("completion artifacts not recorded: %#v", record.Artifacts))
		gomega.Expect(record.Artifacts["summary"]).To(gomega.Equal(summaryPath), fmt.Sprintf("completion artifacts not recorded: %#v", record.Artifacts))

		completionPath := filepath.Join(root, filepath.FromSlash(record.Artifacts["completion"]))
		if _, err := os.Stat(completionPath); err != nil {
			ginkgo.Fail(fmt.Sprintf("completion artifact missing: %v", err))
		}
		var saved runrecord.RunCompletionRecord
		savedBody, err := os.ReadFile(completionPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
		gomega.Expect(json.Unmarshal(savedBody, &saved)).To(gomega.Succeed())
		gomega.Expect(string(savedBody)).NotTo(gomega.ContainSubstring(`"answer"`), fmt.Sprintf("durable completion record should not persist raw answer: %s", string(savedBody)))
		gomega.Expect(saved.Artifacts["completion"]).NotTo(gomega.Equal(""), fmt.Sprintf("saved completion should be self-referential: %#v", saved.Artifacts))

		if got := saved.Metadata["active_mode"]; len(got) != 1 || got[0] != "headless_prompt" {
			ginkgo.Fail(fmt.Sprintf("completion metadata should record active headless mode: %#v", saved.Metadata))
		}
		if got := saved.Metadata["timeout_owner"]; len(got) != 1 || got[0] != "headless_policy_timeout" {
			ginkgo.Fail(fmt.Sprintf("completion metadata should record timeout owner: %#v", saved.Metadata))
		}
		if got := saved.Metadata["build_version"]; len(got) != 1 || got[0] == "" {
			ginkgo.Fail(fmt.Sprintf("completion metadata should record build identity: %#v", saved.Metadata))
		}
		if _, err := os.Stat(summaryPath); err != nil {
			ginkgo.Fail(fmt.Sprintf("summary artifact missing: %v", err))
		}
	})
	ginkgo.It("run help returns zero", func() {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--help"}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			ginkgo.Fail("prompt runner should not run for help")
			return "", nil
		}, nil, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("help exit = %d stderr=%s", code, stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("Usage of idiom run"), fmt.Sprintf("help output missing usage: %q", stderr.String()))

	})
	ginkgo.It("run prompt failure writes completion", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)

		var stdout, stderr bytes.Buffer
		code := Run(
			[]string{"--config", configPath, "--prompt", "fail this"},
			bytes.NewReader(nil),
			&stdout,
			&stderr,
			func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				return "", errors.New("prompt failed")
			},
			nil,
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected prompt failure, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("prompt failed"), fmt.Sprintf("expected prompt failure, code=%d stderr=%s", code, stderr.String()))

		record, _ := readSingleHeadlessCompletion(root)
		gomega.Expect(record.TerminalClass).To(gomega.Equal(runrecord.TerminalClassFailed), fmt.Sprintf("unexpected failure completion: %#v", record))
		gomega.Expect(record.ExitCode).To(gomega.Equal(1), fmt.Sprintf("unexpected failure completion: %#v", record))
		gomega.Expect(record.Error).To(gomega.ContainSubstring("prompt failed"), fmt.Sprintf("unexpected failure completion: %#v", record))

	})
	ginkgo.It("run turns file uses single session runner", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)
		turnsPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "turns.json")
		gomega.Expect(os.WriteFile(turnsPath, []byte(`["first turn","second turn"]`), 0o644)).To(gomega.Succeed())

		var (
			stdout, stderr bytes.Buffer
			gotPrompts     []string
			gotOpts        cmdruntime.RunOptions
		)
		code := Run(
			[]string{"--config", configPath, "--turns-file", turnsPath, "--json", "--resume", "prior-session"},
			bytes.NewReader(nil),
			&stdout,
			&stderr,
			func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("single prompt runner should not be called for --turns-file")
				return "", nil
			},
			func(_ config.Config, prompts []string, opts cmdruntime.RunOptions, w io.Writer) ([]string, error) {
				gotPrompts = append([]string(nil), prompts...)
				gotOpts = opts
				_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"second answer\"}\n"))

				return []string{"first answer", "second answer"}, nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("Run exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(strings.Join(gotPrompts, "|")).To(gomega.Equal("first turn|second turn"), fmt.Sprintf("unexpected prompts: %#v", gotPrompts))
		gomega.Expect(gotOpts.JSONEvents).To(gomega.BeTrue(), fmt.Sprintf("unexpected opts: %#v", gotOpts))
		gomega.Expect(gotOpts.Resume).To(gomega.Equal("prior-session"), fmt.Sprintf("unexpected opts: %#v", gotOpts))

		if got := stdout.String(); got != "{\"type\":\"final\",\"content\":\"second answer\"}\n" {
			ginkgo.Fail(fmt.Sprintf("JSON turns mode must only emit runner JSONL, got %q", got))
		}

		record, _ := readSingleHeadlessCompletion(root)
		gomega.Expect(record.Implementation).To(gomega.Equal("idiom run"), fmt.Sprintf("unexpected turns-file completion: %#v", record))
		gomega.Expect(record.OutputMode).To(gomega.Equal("jsonl"), fmt.Sprintf("unexpected turns-file completion: %#v", record))
		gomega.Expect(record.TerminalClass).To(gomega.Equal(runrecord.TerminalClassCompleted), fmt.Sprintf("unexpected turns-file completion: %#v", record))
		gomega.Expect(record.PromptSHA256).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected turns-file completion: %#v", record))
		gomega.Expect(record.AnswerSHA256).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected turns-file completion: %#v", record))

		if got := record.Metadata["entrypoint"]; len(got) != 1 || got[0] != "idiom run" {
			ginkgo.Fail(fmt.Sprintf("entrypoint metadata = %#v", record.Metadata))
		}
		if got := record.Metadata["active_mode"]; len(got) != 1 || got[0] != "headless_turns_file" {
			ginkgo.Fail(fmt.Sprintf("active_mode metadata = %#v", record.Metadata))
		}
		if got := record.Metadata["turns_file"]; len(got) != 1 || got[0] != filepath.Clean(turnsPath) {
			ginkgo.Fail(fmt.Sprintf("turns_file metadata = %#v", record.Metadata))
		}
	})
	ginkgo.It("run rejects prompt turns conflict", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)
		turnsPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "turns.txt")
		gomega.Expect(os.WriteFile(turnsPath, []byte("first\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer
		code := Run(
			[]string{"--config", configPath, "--prompt", "x", "--turns-file", turnsPath},
			bytes.NewReader(nil),
			&stdout,
			&stderr,
			func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("prompt runner should not run for invalid flags")
				return "", nil
			},
			func(config.Config, []string, cmdruntime.RunOptions, io.Writer) ([]string, error) {
				ginkgo.Fail("turns runner should not run for invalid flags")
				return nil, nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected prompt/turns conflict, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("cannot be combined"), fmt.Sprintf("expected prompt/turns conflict, code=%d stderr=%s", code, stderr.String()))

	})
	ginkgo.It("run validates output schema", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)

		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"--config", configPath,
			"--prompt", "hello",
			"--output", "json",
			"--output-schema", "answer.v1",
			"--session", "run-schema",
		}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			return `{"schema_version":"answer.v1","answer":"ok"}`, nil
		}, nil, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("schema-valid run failed: code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		var output runrecord.RunOutputRecord
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &output)).To(gomega.Succeed())

		record := output.RunCompletionRecord
		gomega.Expect(record.OutputSchema).To(gomega.Equal("answer.v1"), fmt.Sprintf("unexpected schema-valid record: %#v", record))
		gomega.Expect(record.TerminalClass).To(gomega.Equal(runrecord.TerminalClassCompleted), fmt.Sprintf("unexpected schema-valid record: %#v", record))
		gomega.Expect(output.Answer).NotTo(gomega.Equal(""), "schema-valid JSON output should include answer")

		stdout.Reset()
		stderr.Reset()
		code = Run([]string{
			"--config", configPath,
			"--prompt", "hello",
			"--output", "json",
			"--output-schema", "answer.v1",
			"--session", "run-schema-bad",
		}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			return `{"schema_version":"wrong.v1","answer":"ok"}`, nil
		}, nil, nil)
		gomega.Expect(code).NotTo(gomega.Equal(0), "schema mismatch should fail")
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &output)).To(gomega.Succeed())

		record = output.RunCompletionRecord
		gomega.Expect(record.TerminalClass).To(gomega.Equal(runrecord.TerminalClassOutputSchemaFailed), fmt.Sprintf("unexpected schema failure record: %#v", record))

	})
	ginkgo.DescribeTable("rejects invalid policy flags",
		func(flagName, value string) {
			configPath := writeHeadlessTestConfig(ginkgo.GinkgoT().TempDir())
			var stdout, stderr bytes.Buffer
			code := Run([]string{"--config", configPath, "--prompt", "x", flagName, value}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("prompt runner should not run for invalid flags")
				return "", nil
			}, nil, nil)
			gomega.Expect(code).To(gomega.Equal(2), "stdout=%s stderr=%s", stdout.String(), stderr.String())
			gomega.Expect(stderr.String()).To(gomega.ContainSubstring(flagName))
		},
		ginkgo.Entry("permission profile", "--permission-profile", "god-mode"),
		ginkgo.Entry("timeout", "--timeout", "not-a-duration"),
		ginkgo.Entry("max tool steps", "--max-tool-steps", "-1"),
	)
	ginkgo.DescribeTable("rejects invalid session IDs before model execution",
		func(sessionID string) {
			configPath := writeHeadlessTestConfig(ginkgo.GinkgoT().TempDir())
			var stdout, stderr bytes.Buffer
			code := Run([]string{
				"--config", configPath,
				"--prompt", "hello",
				"--output", "json",
				"--session", sessionID,
			}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("prompt runner should not run for invalid session id")
				return "", nil
			}, nil, nil)
			gomega.Expect(code).To(gomega.Equal(2), "stdout=%s stderr=%s", stdout.String(), stderr.String())
			gomega.Expect(stderr.String()).To(gomega.ContainSubstring("invalid run id"))
		},
		ginkgo.Entry("parent traversal", "../escape"),
		ginkgo.Entry("forward slash", "a/b"),
		ginkgo.Entry("backslash", `a\b`),
		ginkgo.Entry("overlong identifier", strings.Repeat("a", 129)),
	)
	ginkgo.It("run fails when completion cannot be saved", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom"), []byte("not a directory"), 0o600)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"--config", configPath,
			"--prompt", "hello",
			"--output", "json",
			"--session", "completion-save-fails",
		}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			return "ok", nil
		}, nil, nil)
		gomega.Expect(code).NotTo(gomega.Equal(0), "completion save failure should fail the run")

		var output runrecord.RunOutputRecord
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &output)).To(gomega.Succeed())
		gomega.Expect(output.TerminalClass).To(gomega.Equal(runrecord.TerminalClassArtifactFailed), fmt.Sprintf("unexpected artifact failure record: %#v", output.RunCompletionRecord))
		gomega.Expect(output.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected artifact failure record: %#v", output.RunCompletionRecord))
		gomega.Expect(output.Artifacts["completion"]).To(gomega.Equal(""), fmt.Sprintf("failed completion save must not advertise a saved completion artifact: %#v", output.Artifacts))

	})
	ginkgo.It("run policy timeout projects to runtime request timeout", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)

		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"--config", configPath,
			"--prompt", "bounded review",
			"--output", "json",
			"--timeout", "2s",
			"--session", "run-timeout-projection",
		}, bytes.NewReader(nil), &stdout, &stderr, func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gomega.Expect(config.Default().Chat.RequestTimeoutSeconds).NotTo(gomega.Equal(2), "test precondition invalid: default request timeout should differ from override")
			gomega.Expect(cfg.Chat.RequestTimeoutSeconds).To(gomega.Equal(2), fmt.Sprintf("timeout projection = request %d start %d, want 2/0", cfg.Chat.RequestTimeoutSeconds, cfg.Chat.ServerStartTimeoutSecs))
			gomega.Expect(cfg.Chat.ServerStartTimeoutSecs).To(gomega.BeZero(), fmt.Sprintf("timeout projection = request %d start %d, want 2/0", cfg.Chat.RequestTimeoutSeconds, cfg.Chat.ServerStartTimeoutSecs))

			return "ok", nil
		}, nil, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("timeout projection run failed stdout=%s stderr=%s", stdout.String(), stderr.String()))

		var output runrecord.RunOutputRecord
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &output)).To(gomega.Succeed())

		if got := output.Metadata["timeout_owner"]; len(got) != 1 || got[0] != "headless_policy_timeout" {
			ginkgo.Fail(fmt.Sprintf("timeout owner metadata should identify policy timeout: %#v", output.Metadata))
		}
	})
	ginkgo.It("run command captures stdout stderr and exit code", func() {
		root := ginkgo.GinkgoT().TempDir()
		check := runCommand(context.Background(), "check", "printf 'out'; printf 'err' >&2; exit 7", headlessCommandTestConfig(root))
		gomega.Expect(check.ExitCode).To(gomega.Equal(7), fmt.Sprintf("ExitCode = %d, want 7; check=%#v", check.ExitCode, check))
		gomega.Expect(check.Stdout).To(gomega.Equal("out"), fmt.Sprintf("Stdout = %q, want out", check.Stdout))
		gomega.Expect(check.Stderr).To(gomega.Equal("err"), fmt.Sprintf("Stderr = %q, want err", check.Stderr))
		gomega.Expect(check.Name).To(gomega.Equal("check"), fmt.Sprintf("check identity not preserved: %#v", check))
		gomega.Expect(check.Command).NotTo(gomega.BeEmpty(), fmt.Sprintf("check identity not preserved: %#v", check))

	})
	ginkgo.It("run command uses configured command dir and bounds output", func() {
		root := ginkgo.GinkgoT().TempDir()
		check := runCommand(context.Background(), "pwd", "pwd; printf '%05000d' 0", headlessCommandTestConfig(root))
		gomega.Expect(check.ExitCode).To(gomega.Equal(0), fmt.Sprintf("ExitCode = %d, want 0; check=%#v", check.ExitCode, check))
		gomega.Expect(strings.HasPrefix(check.Stdout, root)).To(gomega.BeTrue(), fmt.Sprintf("Stdout should start with configured cwd %q, got %q", root, check.Stdout[:min(len(check.Stdout), 80)]))
		gomega.Expect(check.Stdout).To(gomega.HaveLen(4000), fmt.Sprintf("Stdout length = %d, want bounded 4000", len(check.Stdout)))

	})
	ginkgo.It("run command honors context timeout", func() {
		root := ginkgo.GinkgoT().TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		check := runCommand(ctx, "timeout", "while true; do :; done", headlessCommandTestConfig(root))
		gomega.Expect(check.ExitCode).NotTo(gomega.Equal(0), fmt.Sprintf("timeout command should not exit 0: %#v", check))
		gomega.Expect(check.DurationMS).To(gomega.BeNumerically(">", 0), fmt.Sprintf("timeout duration should be bounded, got %dms in %#v", check.DurationMS, check))
		gomega.Expect(check.DurationMS).To(gomega.BeNumerically("<=", 1000), fmt.Sprintf("timeout duration should be bounded, got %dms in %#v", check.DurationMS, check))

	})
	ginkgo.It("write git diff writes workspace patch", func() {
		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip("git not available")
		}

		root := ginkgo.GinkgoT().TempDir()
		runHeadlessGit(root, "init")
		runHeadlessGit(root, "config", "user.email", "test@example.com")
		runHeadlessGit(root, "config", "user.name", "Test User")
		gomega.Expect(os.WriteFile(filepath.Join(root, "file.txt"), []byte("before\n"), 0o644)).To(gomega.Succeed())

		runHeadlessGit(root, "add", "file.txt")
		runHeadlessGit(root, "commit", "-m", "initial")
		gomega.Expect(os.WriteFile(filepath.Join(root, "file.txt"), []byte("after\n"), 0o644)).To(gomega.Succeed())

		patchPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "diff.patch")
		gomega.Expect(writeGitDiff(patchPath, headlessCommandTestConfig(root))).To(gomega.Succeed())

		body, err := os.ReadFile(patchPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read patch: %v", err))

		text := string(body)
		for _, want := range []string{"diff --git a/file.txt b/file.txt", "-before", "+after"} {
			gomega.Expect(text).To(gomega.ContainSubstring(want), fmt.Sprintf("patch missing %q:\n%s", want, text))

		}

		info, err := os.Stat(patchPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("stat patch: %v", err))

		if got := info.Mode().Perm(); got != 0o600 {
			ginkgo.Fail(fmt.Sprintf("patch mode = %o, want 600", got))
		}
	})
	ginkgo.It("run fail on degraded rejects unsupported approval policy", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)

		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"--config", configPath,
			"--prompt", "hello",
			"--output", "json",
			"--approval-policy", "on-request",
			"--fail-on-degraded",
			"--session", "run-degraded",
		}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			return "ok", nil
		}, nil, nil)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("degraded run should fail when --fail-on-degraded is set"))

		var record runrecord.RunCompletionRecord
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &record)).To(gomega.Succeed())
		gomega.Expect(record.TerminalClass).To(gomega.Equal(runrecord.TerminalClassDegraded), fmt.Sprintf("unexpected degraded record: %#v", record))
		gomega.Expect(record.UnsupportedFlags).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected degraded record: %#v", record))

	})
	ginkgo.It("run json flag emits jsonl only", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)

		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"--config", configPath,
			"--prompt", "hello",
			"--json",
			"--session", "run-jsonl",
		}, bytes.NewReader(nil), &stdout, &stderr, func(_ config.Config, _ string, opts cmdruntime.RunOptions, w io.Writer) (string, error) {
			gomega.Expect(opts.JSONEvents).To(gomega.BeTrue(), "--json should request runtime JSON events")

			_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"ok\"}\n"))

			return "ok", nil
		}, nil, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("Run exit = %d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("\nok\n"), fmt.Sprintf("JSONL mode should not append plain answer: %q", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"final"`), fmt.Sprintf("expected runtime JSONL event, got %q", stdout.String()))

	})
	ginkgo.It("session export sanitizes and replay returns prompts", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)
		logDir := filepath.Join(root, "logs", "conversations")
		gomega.Expect(os.MkdirAll(logDir, 0o755)).To(gomega.Succeed())

		logPath := filepath.Join(logDir, "session-1.jsonl")
		gomega.Expect(os.WriteFile(logPath, []byte(
			`{"type":"message","role":"user","content":"secret prompt"}`+"\n"+
				`{"type":"tool_result","tool":"read_file","summary":"read README","content":"raw file body"}`+"\n",
		), 0o600)).To(gomega.Succeed())

		exportPath := filepath.Join(root, "export.json")
		var stdout, stderr bytes.Buffer
		code := RunSession([]string{"export", "--config", configPath, "--session", "session-1", "--out", exportPath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("export exit = %d stderr=%s", code, stderr.String()))

		body, err := os.ReadFile(exportPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
		gomega.Expect(string(body)).NotTo(gomega.ContainSubstring("secret prompt"), fmt.Sprintf("sanitized export leaked raw content: %s", string(body)))
		gomega.Expect(string(body)).NotTo(gomega.ContainSubstring("raw file body"), fmt.Sprintf("sanitized export leaked raw content: %s", string(body)))
		gomega.Expect(string(body)).To(gomega.ContainSubstring("read README"), fmt.Sprintf("sanitized export should retain summaries: %s", string(body)))

		stdout.Reset()
		stderr.Reset()
		code = RunSession([]string{"replay", "--config", configPath, "--session", "session-1"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("replay exit = %d stderr=%s", code, stderr.String()))
		gomega.Expect(strings.TrimSpace(stdout.String())).To(gomega.Equal("secret prompt"), fmt.Sprintf("replay stdout = %q", stdout.String()))

	})
	ginkgo.It("contract inspect reports configured policy", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeHeadlessTestConfig(root)

		var stdout, stderr bytes.Buffer
		code := RunContract([]string{"inspect", "--config", configPath, "--json"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("contract inspect exit = %d stderr=%s", code, stderr.String()))

		var report map[string]any
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report["schema_version"]).To(gomega.Equal("idiom.headless.contract_report.v1"), fmt.Sprintf("unexpected report: %#v", report))

		gotRoot, _ := filepath.EvalSymlinks(report["workspace_root"].(string))
		wantRoot, _ := filepath.EvalSymlinks(root)
		gomega.Expect(gotRoot).To(gomega.Equal(wantRoot), fmt.Sprintf("workspace root = %v, want %s", report["workspace_root"], root))

	})
	ginkgo.It("serve mux reports health and status", func() {
		server := httptest.NewServer(newServeMux())
		defer server.Close()

		for _, path := range []string{"/healthz", "/v1/status"} {
			resp, err := http.Get(server.URL + path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("GET %s: %v", path, err))

			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusOK), fmt.Sprintf("GET %s status = %d", path, resp.StatusCode))

			var body map[string]any
			gomega.Expect(json.NewDecoder(resp.Body).Decode(&body)).To(gomega.Succeed())
			gomega.Expect(body["schema_version"]).To(gomega.Equal("idiom.headless.server.v1"), fmt.Sprintf("%s schema_version = %v", path, body["schema_version"]))

		}
	})
})

func headlessCommandTestConfig(root string) config.Config {
	cfg := config.Config{}
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.Shell = "/bin/sh"

	return cfg
}

func runHeadlessGit(dir string, args ...string) {

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		ginkgo.Fail(fmt.Sprintf("git %v failed: %v\n%s", args, err, string(out)))
	}
}

func writeHeadlessTestConfig(root string) string {

	cfg := config.Default()
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.Shell = "/bin/sh"
	cfg.Workspace.EnableShell = true
	cfg.Workspace.EnableWrites = true

	data, err := json.Marshal(cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	configPath := filepath.Join(root, "config.json")
	gomega.Expect(os.WriteFile(configPath, data, 0o600)).To(gomega.Succeed())

	return configPath
}

func readSingleHeadlessCompletion(root string) (runrecord.RunCompletionRecord, []byte) {

	matches, err := filepath.Glob(filepath.Join(root, ".idiom", "runs", "*.completion.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	gomega.Expect(matches).To(gomega.HaveLen(1), fmt.Sprintf("expected one completion record, got %#v", matches))

	body, err := os.ReadFile(matches[0])
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	var record runrecord.RunCompletionRecord
	gomega.Expect(json.Unmarshal(body, &record)).To(gomega.Succeed())

	return record, body
}
