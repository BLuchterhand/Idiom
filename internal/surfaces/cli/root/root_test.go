package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/sessionlog"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	toolcmd "idiom/internal/surfaces/cli/tool"
	"idiom/internal/workflows/recovery"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func nopList(config.Config, io.Writer) error { return nil }

func runRuntime(args []string, stdout io.Writer, stderr io.Writer, tuiRun cmdruntime.ModeRunner, serverRun cmdruntime.ModeRunner, listSessions cmdruntime.ListSessionsRunner) int {
	return cmdruntime.RunWithTelemetry(args, stdout, stderr, tuiRun, serverRun, listSessions, runVersionMain, nil)
}

var _ = ginkgo.Describe("Runtime mode routing", func() {
	ginkgo.It("calls the list-sessions runner exclusively", func() {
		t := ginkgo.GinkgoT()
		configPath := writeMainConfig(t)
		var stdout, stderr bytes.Buffer
		called := false

		code := runRuntime(
			[]string{"-config", configPath, "-list-sessions"},
			&stdout,
			&stderr,
			func(config.Config, cmdruntime.RunOptions) error {
				ginkgo.Fail("TUI runner should not be called")
				return nil
			},
			func(config.Config, cmdruntime.RunOptions) error {
				ginkgo.Fail("server runner should not be called")
				return nil
			},
			func(_ config.Config, writer io.Writer) error {
				called = true
				_, err := writer.Write([]byte("session-list\n"))
				return err
			},
		)

		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(called).To(gomega.BeTrue())
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("session-list"))
	})

	ginkgo.It("uses the injected server runner in server-only mode", func() {
		configPath := writeMainConfig(ginkgo.GinkgoT())
		var stdout, stderr bytes.Buffer
		called := false

		code := runRuntime(
			[]string{"-config", configPath, "-server-only"},
			&stdout,
			&stderr,
			func(config.Config, cmdruntime.RunOptions) error {
				ginkgo.Fail("TUI runner should not be called in server-only mode")
				return nil
			},
			func(config.Config, cmdruntime.RunOptions) error {
				called = true
				return nil
			},
			nopList,
		)

		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(called).To(gomega.BeTrue())
	})

	ginkgo.It("uses the TUI runner by default", func() {
		configPath := writeMainConfig(ginkgo.GinkgoT())
		var stdout, stderr bytes.Buffer
		called := false

		code := runRuntime(
			[]string{"-config", configPath},
			&stdout,
			&stderr,
			func(config.Config, cmdruntime.RunOptions) error {
				called = true
				return nil
			},
			func(config.Config, cmdruntime.RunOptions) error {
				ginkgo.Fail("server runner should not be called in default mode")
				return nil
			},
			nopList,
		)

		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(called).To(gomega.BeTrue())
	})

	ginkgo.It("loads workspace tool rules from the root config", func() {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		configPath := filepath.Join(root, "config.json")
		configJSON := fmt.Sprintf(`{
  "workspace": {
    "root": %q,
    "cwd": %q,
    "tool_rules": {
      "read_file": [
        {"pattern": ".idiom/deny-read-test.txt", "decision": "deny"},
        {"pattern": "*", "decision": "allow"}
      ]
    }
  }
}`, root, root)
		gomega.Expect(os.WriteFile(configPath, []byte(configJSON+"\n"), 0o644)).To(gomega.Succeed())

		var (
			stdout, stderr bytes.Buffer
			gotCfg         config.Config
		)
		code := runRuntime(
			[]string{"-config", configPath},
			&stdout,
			&stderr,
			func(cfg config.Config, _ cmdruntime.RunOptions) error {
				gotCfg = cfg
				return nil
			},
			func(config.Config, cmdruntime.RunOptions) error {
				ginkgo.Fail("server runner should not be called in default mode")
				return nil
			},
			nopList,
		)

		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		manager := workspace.NewManager(gotCfg.Workspace)
		decision := manager.EvaluateConfiguredToolRule(workspace.ToolRequest{Tool: "read_file", Path: ".idiom/deny-read-test.txt"})
		gomega.Expect(decision.Decision).To(gomega.Equal(toolpolicy.Deny), "rules=%#v", gotCfg.Workspace.ToolRules)
	})

	ginkgo.It("reports malformed configuration", func() {
		badPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "bad.json")
		gomega.Expect(os.WriteFile(badPath, []byte(`{invalid json}`), 0o644)).To(gomega.Succeed())
		var stdout, stderr bytes.Buffer

		code := runRuntime(
			[]string{"-config", badPath},
			&stdout,
			&stderr,
			func(config.Config, cmdruntime.RunOptions) error { return nil },
			func(config.Config, cmdruntime.RunOptions) error { return nil },
			nopList,
		)

		gomega.Expect(code).To(gomega.Equal(1))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("inspect config"))
	})

	ginkgo.It("rejects unknown flags as a usage error", func() {
		var stdout, stderr bytes.Buffer
		code := runRuntime(
			[]string{"-does-not-exist"},
			&stdout,
			&stderr,
			func(config.Config, cmdruntime.RunOptions) error { return nil },
			func(config.Config, cmdruntime.RunOptions) error { return nil },
			nopList,
		)

		gomega.Expect(code).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("flag provided but not defined"))
	})

	ginkgo.It("short-circuits all runtime runners for --version", func() {
		var stdout, stderr bytes.Buffer
		code := runRuntime(
			[]string{"--version"},
			&stdout,
			&stderr,
			func(config.Config, cmdruntime.RunOptions) error {
				ginkgo.Fail("TUI runner must not be called for --version")
				return nil
			},
			func(config.Config, cmdruntime.RunOptions) error {
				ginkgo.Fail("server runner must not be called for --version")
				return nil
			},
			func(config.Config, io.Writer) error {
				ginkgo.Fail("list-sessions runner must not be called for --version")
				return nil
			},
		)

		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(stdout.String()).To(gomega.And(
			gomega.ContainSubstring("version: "),
			gomega.ContainSubstring("go: "),
			gomega.ContainSubstring("os/arch: "),
		))
	})

	ginkgo.It("accepts the single-dash version spelling", func() {
		var stdout, stderr bytes.Buffer
		code := runRuntime(
			[]string{"-version"},
			&stdout,
			&stderr,
			func(config.Config, cmdruntime.RunOptions) error { return nil },
			func(config.Config, cmdruntime.RunOptions) error { return nil },
			nopList,
		)

		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("version: "))
	})
})

var _ = ginkgo.Describe("Root attribution contracts", func() {
	ginkgo.DescribeTable("derives session IDs from paths",
		func(path, want string) {
			gomega.Expect(sessionlog.SessionIDFromPath(path)).To(gomega.Equal(want))
		},
		ginkgo.Entry("conversation log", "logs/conversations/20260418-055543.504.jsonl", "20260418-055543.504"),
		ginkgo.Entry("absolute path", "/abs/path/sess.jsonl", "sess"),
		ginkgo.Entry("plain identifier", "plain", "plain"),
	)

	ginkgo.DescribeTable("names command telemetry from the routed surface",
		func(args []string, want string) {
			gomega.Expect(rootTelemetryCommandName(args)).To(gomega.Equal(want))
		},
		ginkgo.Entry("empty starts TUI", []string(nil), "tui"),
		ginkgo.Entry("tool subcommand", []string{"tool", "run"}, "tool:run"),
		ginkgo.Entry("tool root", []string{"tool"}, "tool"),
		ginkgo.Entry("MCP subcommand", []string{"mcp", "serve"}, "mcp:serve"),
		ginkgo.Entry("implement", []string{"implement"}, "implement"),
		ginkgo.Entry("plan execute", []string{"plan-execute"}, "plan-execute"),
		ginkgo.Entry("artifact", []string{"artifact"}, "artifact"),
		ginkgo.Entry("recover subcommand", []string{"recover", "resume"}, "recover:resume"),
		ginkgo.Entry("local ops benchmark", []string{"local-ops", "benchmark", "--json"}, "local-ops:benchmark"),
		ginkgo.Entry("product audit", []string{"product", "audit", "--effort-root", "."}, "product:audit"),
		ginkgo.Entry("coding rules check", []string{"coding-rules", "check"}, "coding-rules:check"),
		ginkgo.Entry("queue run", []string{"queue", "run"}, "queue:run"),
		ginkgo.Entry("explicit run", []string{"run", "--prompt=hello"}, "run"),
		ginkgo.Entry("retired prompt fallback", []string{"--prompt=hello"}, "tui"),
		ginkgo.Entry("retired turns-file fallback", []string{"-turns-file=turns.jsonl"}, "tui"),
		ginkgo.Entry("legacy server-only fallback", []string{"--server-only"}, "server"),
		ginkgo.Entry("legacy list-sessions fallback", []string{"-list-sessions"}, "list_sessions"),
		ginkgo.Entry("unknown fallback", []string{"unknown"}, "tui"),
	)
})

var _ = ginkgo.Describe("Direct tool routing", func() {
	ginkgo.It("writes fully attributed telemetry for a successful direct tool", func() {
		t := ginkgo.GinkgoT()
		telemetryDir := t.TempDir()
		t.Setenv("IDIOM_TELEMETRY_DIR", telemetryDir)
		configPath := writeMainConfig(t)
		var stdout, stderr bytes.Buffer

		code := runMain([]string{"tool", "pwd", "-config", configPath, "--caller-id", "cli-smoke", "--request-id", "req-cli", "--trace-id", "trace:cli"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"Tool": "pwd"`))

		entries, err := os.ReadDir(telemetryDir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(1))
		records := readMainTelemetryRecords(t, filepath.Join(telemetryDir, entries[0].Name()))
		gomega.Expect(recordTypesContain(records, "process_start", "tool_request", "tool_result", "process_exit")).To(gomega.BeTrue(), "records=%#v", records)

		var directResult map[string]any
		for _, record := range records {
			if record["type"] == "tool_result" && record["tool"] == "pwd" && record["agent"] == "direct" {
				directResult = record
				break
			}
		}
		gomega.Expect(directResult).To(gomega.SatisfyAll(
			gomega.HaveKeyWithValue("transport", "cli"),
			gomega.HaveKeyWithValue("profile", "default"),
			gomega.HaveKeyWithValue("caller_id", "cli-smoke"),
			gomega.HaveKeyWithValue("request_id", "req-cli"),
			gomega.HaveKeyWithValue("trace_id", "trace:cli"),
			gomega.HaveKeyWithValue("sidecar_schema_version", workspace.ToolSidecarEnvelopeVersion),
			gomega.HaveKeyWithValue("contract_schema_version", workspace.ToolSchemaExportVersion),
			gomega.HaveKeyWithValue("contract_id", "idiom.tool.v1:pwd"),
		))
	})

	ginkgo.It("returns the sidecar envelope without changing legacy fields", func() {
		configPath := writeMainConfig(ginkgo.GinkgoT())
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "pwd", "-config", configPath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())

		var payload struct {
			Tool    string `json:"Tool"`
			Summary string `json:"Summary"`
			Output  string `json:"Output"`
			Error   string `json:"Error"`
			Sidecar struct {
				SchemaVersion    string         `json:"schema_version"`
				Tool             string         `json:"tool"`
				Profile          string         `json:"profile"`
				Contract         map[string]any `json:"contract"`
				Warnings         []any          `json:"warnings"`
				AuditEventIDs    []any          `json:"audit_event_ids"`
				StructuredOutput map[string]any `json:"structured_output"`
				Error            any            `json:"error"`
			} `json:"sidecar"`
		}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &payload)).To(gomega.Succeed(), "output=%s", stdout.String())

		gomega.Expect(payload.Tool).To(gomega.Equal("pwd"))
		gomega.Expect(payload.Summary).NotTo(gomega.BeEmpty())
		gomega.Expect(payload.Output).NotTo(gomega.BeEmpty())
		gomega.Expect(payload.Error).To(gomega.BeEmpty())
		gomega.Expect(payload.Sidecar.SchemaVersion).To(gomega.Equal("idiom.tool.v1"))
		gomega.Expect(payload.Sidecar.Tool).To(gomega.Equal("pwd"))
		gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal("default"))
		gomega.Expect(payload.Sidecar.Contract).To(gomega.SatisfyAll(
			gomega.HaveKeyWithValue("id", "idiom.tool.v1:pwd"),
			gomega.HaveKey("metadata"),
		))
		gomega.Expect(payload.Sidecar.Warnings).NotTo(gomega.BeNil())
		gomega.Expect(payload.Sidecar.AuditEventIDs).NotTo(gomega.BeNil())
		gomega.Expect(payload.Sidecar.StructuredOutput).To(gomega.HaveKeyWithValue("format", "text"))
		gomega.Expect(payload.Sidecar.Error).To(gomega.BeNil())
	})

	ginkgo.It("returns a structured sidecar error for an unknown tool", func() {
		configPath := writeMainConfig(ginkgo.GinkgoT())
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "not_a_tool", "-config", configPath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), "stdout=%s stderr=%s", stdout.String(), stderr.String())

		var payload struct {
			Tool    string `json:"Tool"`
			Summary string `json:"Summary"`
			Error   string `json:"Error"`
			Sidecar struct {
				SchemaVersion string `json:"schema_version"`
				Error         struct {
					Class   string `json:"class"`
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"sidecar"`
		}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &payload)).To(gomega.Succeed(), "output=%s", stdout.String())
		gomega.Expect(payload.Tool).To(gomega.Equal("not_a_tool"))
		gomega.Expect(payload.Summary).NotTo(gomega.BeEmpty())
		gomega.Expect(payload.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(payload.Sidecar.SchemaVersion).To(gomega.Equal("idiom.tool.v1"))
		gomega.Expect(payload.Sidecar.Error.Class).To(gomega.Equal("unsupported_tool"))
		gomega.Expect(payload.Sidecar.Error.Code).To(gomega.Equal("unsupported_tool"))
		gomega.Expect(payload.Sidecar.Error.Message).To(gomega.ContainSubstring("unsupported tool"))
	})

	ginkgo.It("allows a read-only tool under the discovery profile", func() {
		configPath := writeMainConfig(ginkgo.GinkgoT())
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "context_status", "-config", configPath, "--profile", "discovery", "--input", `{"limit":1}`}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), "stdout=%s stderr=%s", stdout.String(), stderr.String())

		var payload struct {
			Tool    string `json:"Tool"`
			Sidecar struct {
				Profile string `json:"profile"`
				Error   any    `json:"error"`
			} `json:"sidecar"`
		}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &payload)).To(gomega.Succeed(), "output=%s", stdout.String())
		gomega.Expect(payload.Tool).To(gomega.Equal("context_status"))
		gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal("discovery"))
		gomega.Expect(payload.Sidecar.Error).To(gomega.BeNil())
	})

	ginkgo.It("allows recovery inspection under the discovery profile", func() {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		_, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-cli",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		configPath := writeMainConfigRoot(t, root)
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "recovery_inspect", "-config", configPath, "--profile", "discovery", "--input", `{"session_id":"sid-cli"}`}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), "stdout=%s stderr=%s", stdout.String(), stderr.String())

		var payload struct {
			Tool    string `json:"Tool"`
			Error   string `json:"Error"`
			Sidecar struct {
				Profile          string         `json:"profile"`
				Contract         map[string]any `json:"contract"`
				StructuredOutput map[string]any `json:"structured_output"`
				Error            any            `json:"error"`
			} `json:"sidecar"`
		}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &payload)).To(gomega.Succeed(), "output=%s", stdout.String())
		gomega.Expect(payload.Tool).To(gomega.Equal("recovery_inspect"))
		gomega.Expect(payload.Error).To(gomega.BeEmpty())
		gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal("discovery"))
		gomega.Expect(payload.Sidecar.Error).To(gomega.BeNil())
		gomega.Expect(payload.Sidecar.Contract).To(gomega.HaveKeyWithValue("id", "idiom.tool.v1:recovery_inspect"))
		gomega.Expect(payload.Sidecar.StructuredOutput).To(gomega.SatisfyAll(
			gomega.HaveKeyWithValue("failure_id", "plan-execute-sid-cli"),
			gomega.HaveKeyWithValue("next_valid_action", string(recoverycontract.NextActionReviewDecomposition)),
		))
	})

	ginkgo.It("denies context refresh before execution under discovery", func() {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		configPath := writeMainConfigRoot(t, root)
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "context_status", "-config", configPath, "--profile", "discovery", "--input", `{"refresh":true}`}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), "stdout=%s stderr=%s", stdout.String(), stderr.String())

		payload := decodeProfileDenial(stdout.Bytes())
		gomega.Expect(payload.Tool).To(gomega.Equal("context_status"))
		gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal("discovery"))
		gomega.Expect(payload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"))
		gomega.Expect(payload.Sidecar.Error.Code).To(gomega.Equal("profile_denied"))
		gomega.Expect(payload.Sidecar.Error.Message).To(gomega.ContainSubstring("read_only_profile"))

		for _, path := range []string{
			filepath.Join(root, ".idiom", "comprehension"),
			filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"),
		} {
			_, err := os.Stat(path)
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "denied refresh created %s; stat err=%v", path, err)
		}
	})

	ginkgo.It("denies writes before execution under discovery", func() {
		configPath := writeMainConfig(ginkgo.GinkgoT())
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "write_file", "-config", configPath, "--profile", "discovery", "--input", `{"path":"profile-denied.txt","content":"nope"}`}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), "stdout=%s stderr=%s", stdout.String(), stderr.String())

		payload := decodeProfileDenial(stdout.Bytes())
		gomega.Expect(payload.Tool).To(gomega.Equal("write_file"))
		gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal("discovery"))
		gomega.Expect(payload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"))
		gomega.Expect(payload.Sidecar.Error.Code).To(gomega.Equal("profile_denied"))
		gomega.Expect(payload.Sidecar.Error.Message).To(gomega.ContainSubstring("read_only_profile"))
		_, err := os.Stat(filepath.Join(filepath.Dir(configPath), "profile-denied.txt"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "write_file appears to have executed; stat err=%v", err)
	})

	ginkgo.It("rejects an unknown tool profile as a usage error", func() {
		configPath := writeMainConfig(ginkgo.GinkgoT())
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "pwd", "-config", configPath, "--profile", "verify"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(2), "stdout=%s stderr=%s", stdout.String(), stderr.String())
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unknown tool profile"))
	})

	ginkgo.It("writes telemetry for rejected direct-tool input", func() {
		t := ginkgo.GinkgoT()
		telemetryDir := t.TempDir()
		t.Setenv("IDIOM_TELEMETRY_DIR", telemetryDir)
		configPath := writeMainConfig(t)
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "harness_feedback", "-config", configPath, "-input", "{"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), "stdout=%s stderr=%s", stdout.String(), stderr.String())
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("invalid input JSON"))

		entries, err := os.ReadDir(telemetryDir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(1))
		records := readMainTelemetryRecords(t, filepath.Join(telemetryDir, entries[0].Name()))
		gomega.Expect(recordTypesContain(records, "process_start", "tool_request", "tool_result", "process_exit")).To(gomega.BeTrue(), "records=%#v", records)

		gomega.Expect(records).To(gomega.ContainElement(gomega.And(
			gomega.HaveKeyWithValue("type", "tool_result"),
			gomega.HaveKeyWithValue("tool", "harness_feedback"),
			gomega.HaveKeyWithValue("error", gomega.ContainSubstring("invalid input JSON")),
		)))
	})

	ginkgo.It("uses the shared request decoder for direct-tool input", func() {
		t := ginkgo.GinkgoT()
		configPath := filepath.Join(t.TempDir(), "config.json")
		repoRoot := rootTestRepoRoot(t)
		configJSON := fmt.Sprintf(`{"workspace":{"root":%q}}`, repoRoot)
		gomega.Expect(os.WriteFile(configPath, []byte(configJSON+"\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer
		code := runMain([]string{
			"tool", "go_read_symbol",
			"-config", configPath,
			"-input", `{"path":"internal/surfaces/cli/root/root.go","pattern":"runMain"}`,
		}, &stdout, &stderr)

		gomega.Expect(code).To(gomega.Equal(0), "stdout=%s stderr=%s", stdout.String(), stderr.String())
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("runMain"))
	})
})

var _ = ginkgo.Describe("Tool discovery surfaces", func() {
	ginkgo.It("prints manifest-derived help without parser noise", func() {
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "harness_feedback", "--help"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(stdout.String()).To(gomega.And(
			gomega.ContainSubstring("Usage:"),
			gomega.ContainSubstring("Record structured feedback"),
			gomega.ContainSubstring(`"confidence"`),
			gomega.ContainSubstring("Example:"),
		))
		gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring("flag: help requested"))
	})

	ginkgo.It("exports manifest-derived schemas and policy metadata", func() {
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"tool", "schemas"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())

		var export struct {
			SchemaVersion string `json:"schema_version"`
			Source        string `json:"source"`
			Metadata      struct {
				ClientClassifications []string `json:"client_classifications"`
				ProductSignals        []string `json:"product_signals"`
			} `json:"metadata"`
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"input_schema"`
				Profiles    []string        `json:"profiles"`
				Policy      struct {
					SideEffects       []string `json:"side_effects"`
					DecisionAuthority string   `json:"decision_authority"`
					ErrorClasses      []string `json:"error_classes"`
				} `json:"policy"`
			} `json:"tools"`
		}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &export)).To(gomega.Succeed(), "output=%s", stdout.String())
		gomega.Expect(export.SchemaVersion).NotTo(gomega.BeEmpty())
		gomega.Expect(export.Source).To(gomega.ContainSubstring("BuildToolManifest"))
		gomega.Expect(export.Metadata.ClientClassifications).To(gomega.HaveLen(7))
		gomega.Expect(export.Metadata.ProductSignals).To(gomega.HaveLen(9))

		var (
			sawReadFile     bool
			sawContextQuery bool
		)
		for _, tool := range export.Tools {
			switch tool.Name {
			case "context_query":
				sawContextQuery = true
				gomega.Expect(tool.Policy.ErrorClasses).To(gomega.ContainElement("stale_context"))
				gomega.Expect(string(tool.InputSchema)).To(gomega.And(
					gomega.ContainSubstring(`"freshness_policy"`),
					gomega.ContainSubstring(`"freshness_max_files"`),
				))
			case "read_file":
				sawReadFile = true
				gomega.Expect(json.Valid(tool.InputSchema)).To(gomega.BeTrue())
				gomega.Expect(tool.Profiles).NotTo(gomega.BeEmpty())
				gomega.Expect(tool.Policy.SideEffects).NotTo(gomega.BeEmpty())
				gomega.Expect(tool.Policy.DecisionAuthority).To(gomega.Equal("runtime_contract"))
				gomega.Expect(tool.Policy.ErrorClasses).To(gomega.ContainElement("invalid_input"))
			}
		}
		gomega.Expect(sawReadFile).To(gomega.BeTrue(), "tools=%#v", export.Tools)
		gomega.Expect(sawContextQuery).To(gomega.BeTrue(), "tools=%#v", export.Tools)
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("Inspect one focused architecture/review question"))
	})

	ginkgo.It("recognizes help flags but not flag values", func() {
		gomega.Expect(toolcmd.HelpRequested([]string{"--config", "custom.json", "--help"})).To(gomega.BeTrue())
		gomega.Expect(toolcmd.HelpRequested([]string{"--input", "help"})).To(gomega.BeFalse())
	})
})

type profileDenialPayload struct {
	Tool    string `json:"Tool"`
	Error   string `json:"Error"`
	Sidecar struct {
		Profile string `json:"profile"`
		Error   struct {
			Class   string `json:"class"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"sidecar"`
}

func decodeProfileDenial(data []byte) profileDenialPayload {
	var payload profileDenialPayload
	gomega.Expect(json.Unmarshal(data, &payload)).To(gomega.Succeed(), "output=%s", string(data))
	return payload
}

func rootTestRepoRoot(t rootTestContext) string {
	t.Helper()
	dir, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			ginkgo.Fail("repository root not found from working directory")
			return ""
		}
		dir = parent
	}
}

func writeMainConfig(t rootTestContext) string {
	t.Helper()
	return writeMainConfigRoot(t, t.TempDir())
}

func writeMainConfigRoot(t rootTestContext, root string) string {
	t.Helper()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
	path := filepath.Join(t.TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	gomega.Expect(os.WriteFile(path, []byte(configJSON+"\n"), 0o644)).To(gomega.Succeed())
	return path
}

func readMainTelemetryRecords(t rootTestContext, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var record map[string]any
		gomega.Expect(json.Unmarshal([]byte(line), &record)).To(gomega.Succeed(), "line=%q", line)
		records = append(records, record)
	}
	return records
}

func recordTypesContain(records []map[string]any, want ...string) bool {
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if typ, ok := record["type"].(string); ok {
			seen[typ] = true
		}
	}

	for _, typ := range want {
		if !seen[typ] {
			return false
		}
	}
	return true
}
