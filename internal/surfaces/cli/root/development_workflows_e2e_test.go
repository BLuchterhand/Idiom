package root

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents/jsoneventstest"
	"idiom/internal/runtime/backends"
	"idiom/internal/surfaces/cli/runrecord"
)

const (
	developmentWorkflowAPIKeyEnv = "IDIOM_DEVELOPMENT_E2E_API_KEY"
	developmentWorkflowAPIKey    = "development-e2e-key"
)

var _ = ginkgo.Describe("Compiled software-development workflows", ginkgo.Ordered, ginkgo.Label("e2e", "integration", "smoke", "development-scenario"), func() {
	var bin string

	ginkgo.BeforeAll(func() {
		bin = buildIdiomBinary(ginkgo.GinkgoT())
	})

	ginkgo.It("diagnoses a legacy defect, recovers from a failed repair, and verifies the corrected implementation", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		writeDevelopmentE2EFile(t, workspaceRoot, "go.mod", "module example.com/calculator\n\ngo 1.22\n")
		writeDevelopmentE2EFile(t, workspaceRoot, "calculator.go", strings.Join([]string{
			"package calculator",
			"",
			"// Add returns the sum of two integers.",
			"func Add(left, right int) int {",
			"\treturn left - right",
			"}",
			"",
		}, "\n"))
		writeDevelopmentE2EFile(t, workspaceRoot, "calculator_test.go", strings.Join([]string{
			"package calculator",
			"",
			"import \"testing\"",
			"",
			"func TestAdd(t *testing.T) {",
			"\tif got := Add(2, 3); got != 5 {",
			"\t\tt.Fatalf(\"Add(2, 3) = %d, want 5\", got)",
			"\t}",
			"}",
			"",
		}, "\n"))

		peer := newDevelopmentRepairPeer()
		server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
		ginkgo.DeferCleanup(server.Close)
		configPath := writeDevelopmentE2EConfig(t, workspaceRoot, server.URL, true, true)
		t.Setenv(developmentWorkflowAPIKeyEnv, developmentWorkflowAPIKey)

		prompt := "/implement Fix the failing Add behavior in calculator.go without changing its API. Read the implementation and test, run go test ./..., diagnose any failed repair, correct it, rerun verification, and cite the final source line."
		result := runIdiomBinary(t, bin,
			"run",
			"--config", configPath,
			"--prompt", prompt,
			"--session", "legacy-repair",
			"--output", "json",
			"--permission-profile", "write",
			"--approval-policy", "never",
		)

		requests, auth, peerErrors := peer.snapshot()
		gomega.Expect(peerErrors).To(gomega.BeEmpty(), "requests=%#v", requests)
		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s\nrequests=%#v", result.stdout, result.stderr, requests)
		gomega.Expect(result.stderr).To(gomega.BeEmpty())
		gomega.Expect(auth).To(gomega.HaveEach(gomega.Equal("Bearer " + developmentWorkflowAPIKey)))
		expectDevelopmentRequestMilestonesPresent(requests,
			developmentRequestMilestone{name: "read tool is available", matches: func(request map[string]any) bool {
				return requestContainsNamedTool(request, workspace.ToolNameReadFile)
			}},
			developmentRequestMilestone{name: "edit tool is available", matches: func(request map[string]any) bool {
				return requestContainsNamedTool(request, workspace.ToolNameEditFile)
			}},
			developmentRequestMilestone{name: "verification tool is available", matches: func(request map[string]any) bool {
				return requestContainsNamedTool(request, workspace.ToolNameShell)
			}},
		)
		expectDevelopmentRequestMilestonesInOrder(requests,
			developmentRequestMilestone{name: "failed verification is returned to the model", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-test-fail", "FAIL")
			}},
			developmentRequestMilestone{name: "successful verification is returned after repair", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-test-pass", "ok")
			}},
		)

		finalSource, err := os.ReadFile(filepath.Join(workspaceRoot, "calculator.go"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(finalSource)).To(gomega.ContainSubstring("return left + right"))
		verify := exec.Command("go", "test", "./...")
		verify.Dir = workspaceRoot
		verifyOutput, err := verify.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "independent verification output:\n%s", string(verifyOutput))

		var output runrecord.RunOutputRecord
		gomega.Expect(json.Unmarshal([]byte(result.stdout), &output)).To(gomega.Succeed(), result.stdout)
		gomega.Expect(output.TerminalClass).To(gomega.Equal(runrecord.TerminalClassCompleted))
		gomega.Expect(output.Answer).To(gomega.And(
			gomega.ContainSubstring("Changed paths:\n- calculator.go"),
			gomega.ContainSubstring("Verification: go test ./... passed"),
		), "run output=%+v raw=%s", output, result.stdout)
		completionBody := readDevelopmentE2ECompletion(t, workspaceRoot, "legacy-repair")
		gomega.Expect(completionBody).NotTo(gomega.ContainSubstring(prompt))
		gomega.Expect(completionBody).NotTo(gomega.ContainSubstring("return left + right"))
	})

	ginkgo.It("repairs and verifies an implementation through the compiled lane entrypoint", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		writeDevelopmentE2EFile(t, workspaceRoot, "go.mod", "module example.com/lane-calculator\n\ngo 1.22\n")
		writeDevelopmentE2EFile(t, workspaceRoot, "calculator.go", strings.Join([]string{
			"package calculator",
			"",
			"// Add returns the sum of two integers.",
			"func Add(left, right int) int {",
			"\treturn left - right",
			"}",
			"",
		}, "\n"))
		writeDevelopmentE2EFile(t, workspaceRoot, "calculator_test.go", strings.Join([]string{
			"package calculator",
			"",
			"import \"testing\"",
			"",
			"func TestAdd(t *testing.T) {",
			"\tif got := Add(2, 3); got != 5 {",
			"\t\tt.Fatalf(\"Add(2, 3) = %d, want 5\", got)",
			"\t}",
			"}",
			"",
		}, "\n"))
		briefPath := filepath.Join(workspaceRoot, "repair-brief.md")
		writeDevelopmentE2EFile(t, workspaceRoot, "repair-brief.md", strings.Join([]string{
			"# Repair the calculator implementation",
			"",
			"Use the compiled lane boundary to fix the failing Add behavior in `calculator.go` without changing its public API.",
			"Read `calculator.go` and `calculator_test.go`, run `go test ./...`, diagnose any failed repair, correct it, rerun verification, and cite the final source line.",
			"",
		}, "\n"))

		peer := newDevelopmentRepairPeer()
		server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
		ginkgo.DeferCleanup(server.Close)
		configPath := writeDevelopmentE2EConfig(t, workspaceRoot, server.URL, true, true)

		result := runDevelopmentE2EBinary(t, bin, workspaceRoot, map[string]string{
			developmentWorkflowAPIKeyEnv: developmentWorkflowAPIKey,
		},
			"lane", "implement",
			"--config", configPath,
			"--project-root", workspaceRoot,
			"--title", "compiled-lane-repair",
			"--json",
			"--no-lint",
			"--no-context-pack",
			"--planner-owned-verification=false",
			briefPath,
		)

		requests, auth, peerErrors := peer.snapshot()
		gomega.Expect(peerErrors).To(gomega.BeEmpty())
		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s\nrequests=%#v", result.stdout, result.stderr, requests)
		gomega.Expect(result.stderr).To(gomega.BeEmpty())
		gomega.Expect(auth).To(gomega.HaveEach(gomega.Equal("Bearer " + developmentWorkflowAPIKey)))
		expectDevelopmentRequestMilestonesInOrder(requests,
			developmentRequestMilestone{name: "compiled lane objective reaches the backend", matches: func(request map[string]any) bool {
				return developmentE2ERequestContains(request, "compiled lane boundary")
			}},
			developmentRequestMilestone{name: "failed verification is returned to the lane", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-test-fail", "FAIL")
			}},
			developmentRequestMilestone{name: "successful verification is returned after lane repair", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-test-pass", "ok")
			}},
		)

		events := jsoneventstest.DecodeLines(t, result.stdout)
		start, found := jsoneventstest.FindByType(events, lanecontract.EventName(lanecontract.EventStart))
		gomega.Expect(found).To(gomega.BeTrue(), result.stdout)
		gomega.Expect(start).To(gomega.SatisfyAll(
			gomega.HaveKeyWithValue("mode", string(lanecontract.LaneImplement)),
			gomega.HaveKeyWithValue("title", "compiled-lane-repair"),
		))
		ledger, found := jsoneventstest.FindByType(events, lanecontract.EventName(lanecontract.EventRunLedger))
		gomega.Expect(found).To(gomega.BeTrue(), result.stdout)
		gomega.Expect(ledger).To(gomega.SatisfyAll(
			gomega.HaveKeyWithValue("mode", string(lanecontract.LaneImplement)),
			gomega.HaveKeyWithValue("exit_code", float64(0)),
			gomega.HaveKeyWithValue("final_state", "success"),
		))
		_, degraded := jsoneventstest.FindByType(events, lanecontract.EventName(lanecontract.EventDegraded))
		gomega.Expect(degraded).To(gomega.BeFalse(), result.stdout)

		finalSource, err := os.ReadFile(filepath.Join(workspaceRoot, "calculator.go"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(finalSource)).To(gomega.ContainSubstring("return left + right"))
		verify := exec.Command("go", "test", "./...")
		verify.Dir = workspaceRoot
		verifyOutput, err := verify.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "independent verification output:\n%s", string(verifyOutput))

		historyBody, err := os.ReadFile(filepath.Join(workspaceRoot, ".idiom", "lane-history.jsonl"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(strings.TrimSpace(string(historyBody))).To(gomega.And(
			gomega.ContainSubstring(`"mode":"implement"`),
			gomega.ContainSubstring(`"final_state":"success"`),
		))
	})

	ginkgo.It("plans, applies, reviews, and verifies a change through the compiled plan-execute lane", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		writeDevelopmentE2EFile(t, workspaceRoot, "go.mod", "module example.com/plan-lane-calculator\n\ngo 1.22\n")
		writeDevelopmentE2EFile(t, workspaceRoot, "calculator.go", "package calculator\n\nfunc Add(left, right int) int { return left - right }\n")
		writeDevelopmentE2EFile(t, workspaceRoot, "calculator_test.go", strings.Join([]string{
			"package calculator",
			"",
			"import \"testing\"",
			"",
			"func TestAdd(t *testing.T) {",
			"\tif got := Add(2, 3); got != 5 {",
			"\t\tt.Fatalf(\"Add(2, 3) = %d, want 5\", got)",
			"\t}",
			"}",
			"",
		}, "\n"))
		briefPath := filepath.Join(workspaceRoot, "plan-execute-brief.md")
		writeDevelopmentE2EFile(t, workspaceRoot, "plan-execute-brief.md", strings.Join([]string{
			"# Correct the calculator implementation",
			"",
			"## Files To Modify",
			"",
			"| Path | Required behavior |",
			"|---|---|",
			"| `calculator.go` | Make Add return the sum without changing its public API. |",
			"",
			"## Verification",
			"",
			"Run `go test ./...` after applying the bounded plan.",
			"",
		}, "\n"))

		peer := newSequentialResponsesPeer(
			responsesE2EToolCall("resp-plan-read-source", "call-plan-read-source", "read_file", `{"path":"calculator.go"}`),
			responsesE2EToolCall("resp-plan-read-test", "call-plan-read-test", "read_file", `{"path":"calculator_test.go"}`),
			responsesE2EToolCall("resp-submit-plan", "call-submit-plan", "submit_plan", `{"rationale":"Correct Add while preserving its public API.","edits":[{"path":"calculator.go","old_text":"return left - right","new_text":"return left + right","purpose":"make Add satisfy the existing regression test"}]}`),
			responsesE2EFinal("resp-plan-submitted", "Plan submitted."),
			responsesE2EFinal("resp-plan-review", strings.Join([]string{
				"Decision: pass",
				"",
				"Verification Checklist:",
				"- Required behavior: calculator.go returns left + right and TestAdd passes.",
				"- Scope safety: the changed path is limited to calculator.go.",
				"- Test or command evidence: go test ./... passed.",
			}, "\n")),
		)
		server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
		ginkgo.DeferCleanup(server.Close)
		configPath := writeDevelopmentE2EConfig(t, workspaceRoot, server.URL, true, true)

		result := runDevelopmentE2EBinary(t, bin, workspaceRoot, map[string]string{
			developmentWorkflowAPIKeyEnv: developmentWorkflowAPIKey,
		},
			"lane", "plan-execute",
			"--config", configPath,
			"--project-root", workspaceRoot,
			"--title", "compiled-plan-lane",
			"--json",
			"--no-lint",
			"--no-context-pack",
			briefPath,
		)

		requests, auth, peerErrors := peer.snapshot()
		gomega.Expect(peerErrors).To(gomega.BeEmpty())
		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s\nrequests=%#v", result.stdout, result.stderr, requests)
		gomega.Expect(result.stderr).To(gomega.ContainSubstring("plan-execute: context_pack preloaded"))
		gomega.Expect(auth).To(gomega.HaveEach(gomega.Equal("Bearer " + developmentWorkflowAPIKey)))
		expectDevelopmentRequestMilestonesPresent(requests,
			developmentRequestMilestone{name: "plan submission tool is available", matches: func(request map[string]any) bool {
				return requestContainsNamedTool(request, workspace.ToolNameSubmitPlan)
			}},
		)
		expectDevelopmentRequestMilestonesInOrder(requests,
			developmentRequestMilestone{name: "existing regression test is returned to the planner", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-plan-read-test", "TestAdd")
			}},
			developmentRequestMilestone{name: "review-repair receives the applied plan evidence", matches: func(request map[string]any) bool {
				return developmentE2ERequestContains(request, "review-repair")
			}},
		)

		events := jsoneventstest.DecodeLines(t, result.stdout)
		start, found := jsoneventstest.FindByType(events, lanecontract.EventName(lanecontract.EventStart))
		gomega.Expect(found).To(gomega.BeTrue(), result.stdout)
		gomega.Expect(start).To(gomega.SatisfyAll(
			gomega.HaveKeyWithValue("mode", string(lanecontract.LanePlanExecute)),
			gomega.HaveKeyWithValue("title", "compiled-plan-lane"),
		))
		ledger, found := jsoneventstest.FindByType(events, lanecontract.EventName(lanecontract.EventRunLedger))
		gomega.Expect(found).To(gomega.BeTrue(), result.stdout)
		gomega.Expect(ledger).To(gomega.SatisfyAll(
			gomega.HaveKeyWithValue("mode", string(lanecontract.LanePlanExecute)),
			gomega.HaveKeyWithValue("exit_code", float64(0)),
			gomega.HaveKeyWithValue("final_state", "success"),
		))

		finalSource, err := os.ReadFile(filepath.Join(workspaceRoot, "calculator.go"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(finalSource)).To(gomega.ContainSubstring("return left + right"))
		verify := exec.Command("go", "test", "./...")
		verify.Dir = workspaceRoot
		verifyOutput, err := verify.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "independent verification output:\n%s", string(verifyOutput))

		terminalRecords, err := filepath.Glob(filepath.Join(workspaceRoot, ".idiom", "plan-execute", "sessions", "*.terminal.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(terminalRecords).To(gomega.HaveLen(1))
		terminalBody, err := os.ReadFile(terminalRecords[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var terminal struct {
			Status               string `json:"status"`
			ReviewStatus         string `json:"review_status"`
			VerificationCommands []struct {
				Command string `json:"command"`
			} `json:"verification_commands"`
		}
		gomega.Expect(json.Unmarshal(terminalBody, &terminal)).To(gomega.Succeed())
		gomega.Expect(terminal.Status).To(gomega.Equal("completed"))
		gomega.Expect(terminal.ReviewStatus).To(gomega.Equal("passed"))
		gomega.Expect(terminal.VerificationCommands).To(gomega.ContainElement(gomega.HaveField("Command", "go test ./...")))
	})

	ginkgo.It("fails closed on a malformed backend response and redacts prompt and credential secrets from every artifact", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		promptSecret := "PRIVATE-INCIDENT-4711"
		prompt := "Investigate the failed incident response with api_key=" + promptSecret
		credentialSecret := "backend-key-must-never-be-recorded"
		var requestCount int
		var authorization string
		var mu sync.Mutex
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requestCount++
			authorization = r.Header.Get("Authorization")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"truncated"`))
		}))
		ginkgo.DeferCleanup(server.Close)

		configPath := writeDevelopmentE2EConfig(t, workspaceRoot, server.URL, false, false)
		t.Setenv(developmentWorkflowAPIKeyEnv, credentialSecret)
		result := runIdiomBinary(t, bin,
			"run",
			"--config", configPath,
			"--prompt", prompt,
			"--session", "malformed-backend",
			"--output", "json",
			"--permission-profile", "read-only",
		)

		mu.Lock()
		seenRequests := requestCount
		seenAuthorization := authorization
		mu.Unlock()
		gomega.Expect(seenRequests).To(gomega.Equal(1))
		gomega.Expect(seenAuthorization).To(gomega.Equal("Bearer " + credentialSecret))
		gomega.Expect(result.code).To(gomega.Equal(1), "stdout=%s\nstderr=%s", result.stdout, result.stderr)

		var output runrecord.RunOutputRecord
		gomega.Expect(json.Unmarshal([]byte(result.stdout), &output)).To(gomega.Succeed(), result.stdout)
		gomega.Expect(output.TerminalClass).To(gomega.Equal(runrecord.TerminalClassFailed))
		gomega.Expect(output.Answer).To(gomega.BeEmpty())
		gomega.Expect(output.Error).NotTo(gomega.BeEmpty())

		completionBody := readDevelopmentE2ECompletion(t, workspaceRoot, "malformed-backend")
		for _, privateValue := range []string{promptSecret, credentialSecret} {
			gomega.Expect(result.stdout).NotTo(gomega.ContainSubstring(privateValue))
			gomega.Expect(result.stderr).NotTo(gomega.ContainSubstring(privateValue))
			gomega.Expect(completionBody).NotTo(gomega.ContainSubstring(privateValue))
		}
		gomega.Expect(filepath.WalkDir(workspaceRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}

			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, privateValue := range []string{promptSecret, credentialSecret} {
				gomega.Expect(string(body)).NotTo(gomega.ContainSubstring(privateValue), "private value persisted in %s", path)
			}

			return nil
		})).To(gomega.Succeed())
	})

	ginkgo.It("keeps concurrent repository-analysis sessions and durable completions isolated", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(20*time.Second), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		writeDevelopmentE2EFile(t, workspaceRoot, "ALPHA.md", "ALPHA-CONTRACT: preserve public API compatibility\n")
		writeDevelopmentE2EFile(t, workspaceRoot, "BETA.md", "BETA-CONTRACT: preserve transaction atomicity\n")

		peer := &concurrentAnalysisPeer{}
		server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
		ginkgo.DeferCleanup(server.Close)
		configPath := writeDevelopmentE2EConfig(t, workspaceRoot, server.URL, false, false)
		t.Setenv(developmentWorkflowAPIKeyEnv, developmentWorkflowAPIKey)

		type runningCommand struct {
			command *exec.Cmd
			stdout  bytes.Buffer
			stderr  bytes.Buffer
			wait    chan error
			waited  bool
		}
		commands := make([]*runningCommand, 0, 2)
		for _, session := range []struct {
			id     string
			prompt string
		}{
			{id: "concurrent-alpha", prompt: "Read ALPHA.md and report its contract with an ALPHA.md:1 citation."},
			{id: "concurrent-beta", prompt: "Read BETA.md and report its contract with a BETA.md:1 citation."},
		} {
			running := &runningCommand{wait: make(chan error, 1)}
			running.command = exec.Command(bin,
				"run",
				"--config", configPath,
				"--prompt", session.prompt,
				"--session", session.id,
				"--output", "json",
				"--permission-profile", "read-only",
			)
			running.command.Dir = rootTestRepoRoot(t)
			running.command.Env = rootTestEnvironmentWithOverrides(os.Environ(), map[string]string{
				"HOME":                                 t.TempDir(),
				"IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK": "1",
				"IDIOM_TELEMETRY":                      "0",
				developmentWorkflowAPIKeyEnv:           developmentWorkflowAPIKey,
			})
			running.command.Stdout = &running.stdout
			running.command.Stderr = &running.stderr
			gomega.Expect(running.command.Start()).To(gomega.Succeed())
			go func() { running.wait <- running.command.Wait() }()
			commands = append(commands, running)
		}
		ginkgo.DeferCleanup(func() {
			for _, running := range commands {
				if !running.waited {
					_ = running.command.Process.Kill()
					<-running.wait
				}
			}
		})

		for _, running := range commands {
			var waitErr error
			gomega.Eventually(ctx, running.wait).Should(gomega.Receive(&waitErr))
			running.waited = true
			gomega.Expect(waitErr).NotTo(gomega.HaveOccurred(), "stdout=%s\nstderr=%s", running.stdout.String(), running.stderr.String())
			gomega.Expect(running.stderr.String()).To(gomega.BeEmpty())
		}

		alphaOutput := decodeDevelopmentE2ERunOutput(commands[0].stdout.String())
		betaOutput := decodeDevelopmentE2ERunOutput(commands[1].stdout.String())
		gomega.Expect(alphaOutput.Answer).To(gomega.And(gomega.ContainSubstring("ALPHA-CONTRACT"), gomega.ContainSubstring("ALPHA.md:1")))
		gomega.Expect(alphaOutput.Answer).NotTo(gomega.ContainSubstring("BETA-CONTRACT"))
		gomega.Expect(betaOutput.Answer).To(gomega.And(gomega.ContainSubstring("BETA-CONTRACT"), gomega.ContainSubstring("BETA.md:1")))
		gomega.Expect(betaOutput.Answer).NotTo(gomega.ContainSubstring("ALPHA-CONTRACT"))

		alphaCompletion := readDevelopmentE2ECompletion(t, workspaceRoot, "concurrent-alpha")
		betaCompletion := readDevelopmentE2ECompletion(t, workspaceRoot, "concurrent-beta")
		gomega.Expect(alphaCompletion).NotTo(gomega.Equal(betaCompletion))
		for _, completion := range []string{alphaCompletion, betaCompletion} {
			gomega.Expect(completion).NotTo(gomega.ContainSubstring("ALPHA-CONTRACT"))
			gomega.Expect(completion).NotTo(gomega.ContainSubstring("BETA-CONTRACT"))
		}

		requests, peerErrors := peer.snapshot()
		gomega.Expect(peerErrors).To(gomega.BeEmpty())
		expectDevelopmentRequestMilestonesPresent(requests,
			developmentRequestMilestone{name: "alpha objective is routed independently", matches: func(request map[string]any) bool {
				return developmentE2ERequestContains(request, "ALPHA.md")
			}},
			developmentRequestMilestone{name: "beta objective is routed independently", matches: func(request map[string]any) bool {
				return developmentE2ERequestContains(request, "BETA.md")
			}},
			developmentRequestMilestone{name: "alpha evidence returns only to its owner", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-alpha", "ALPHA-CONTRACT")
			}},
			developmentRequestMilestone{name: "beta evidence returns only to its owner", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-beta", "BETA-CONTRACT")
			}},
		)
	})

	ginkgo.It("carries repository evidence across an iterative multi-turn analysis session", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		writeDevelopmentE2EFile(t, workspaceRoot, "STATE.md", "STATE-CONTRACT: the legacy parser accepts implicit coercion\n")
		writeDevelopmentE2EFile(t, workspaceRoot, "EXPECTED.md", "EXPECTED-CONTRACT: the replacement parser rejects implicit coercion\n")
		turns, err := json.Marshal([]string{
			"Read STATE.md and report the current parser behavior with a STATE.md:1 citation.",
			"Using the evidence from the prior turn, read EXPECTED.md and explain the migration risk with an EXPECTED.md:1 citation.",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		turnsPath := filepath.Join(workspaceRoot, "turns.json")
		gomega.Expect(os.WriteFile(turnsPath, append(turns, '\n'), 0o600)).To(gomega.Succeed())

		peer := newSequentialResponsesPeer(
			responsesE2EToolCall("resp-state-read", "call-state", "read_file", `{"path":"STATE.md"}`),
			responsesE2EFinal("resp-state-final", "STATE-CONTRACT says the legacy parser accepts implicit coercion (`STATE.md:1`)."),
			responsesE2EToolCall("resp-expected-read", "call-expected", "read_file", `{"path":"EXPECTED.md"}`),
			responsesE2EFinal("resp-expected-final", "Migration risk: EXPECTED-CONTRACT rejects implicit coercion, so callers must be updated (`EXPECTED.md:1`), while the prior STATE-CONTRACT documents current behavior."),
		)
		server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
		ginkgo.DeferCleanup(server.Close)
		configPath := writeDevelopmentE2EConfig(t, workspaceRoot, server.URL, false, false)
		t.Setenv(developmentWorkflowAPIKeyEnv, developmentWorkflowAPIKey)

		result := runIdiomBinary(t, bin,
			"run",
			"--config", configPath,
			"--turns-file", turnsPath,
			"--session", "iterative-analysis",
			"--output", "json",
			"--permission-profile", "read-only",
		)
		requests, auth, peerErrors := peer.snapshot()
		gomega.Expect(peerErrors).To(gomega.BeEmpty())
		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s\nrequests=%#v", result.stdout, result.stderr, requests)
		gomega.Expect(result.stderr).To(gomega.BeEmpty())
		gomega.Expect(auth).To(gomega.HaveEach(gomega.Equal("Bearer " + developmentWorkflowAPIKey)))
		expectDevelopmentRequestMilestonesInOrder(requests,
			developmentRequestMilestone{name: "first turn receives current-state evidence", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-state", "STATE-CONTRACT")
			}},
			developmentRequestMilestone{name: "second turn joins prior evidence with the expected-state objective", matches: func(request map[string]any) bool {
				return developmentE2ERequestContains(request, "STATE-CONTRACT") && developmentE2ERequestContains(request, "EXPECTED.md")
			}},
			developmentRequestMilestone{name: "second turn receives expected-state evidence", matches: func(request map[string]any) bool {
				return requestContainsFunctionOutput(request, "call-expected", "EXPECTED-CONTRACT")
			}},
		)

		output := decodeDevelopmentE2ERunOutput(result.stdout)
		gomega.Expect(output.TerminalClass).To(gomega.Equal(runrecord.TerminalClassCompleted))
		gomega.Expect(output.Answer).To(gomega.And(
			gomega.ContainSubstring("STATE-CONTRACT"),
			gomega.ContainSubstring("EXPECTED-CONTRACT"),
			gomega.ContainSubstring("Migration risk"),
		))
		completionBody := readDevelopmentE2ECompletion(t, workspaceRoot, "iterative-analysis")
		gomega.Expect(completionBody).NotTo(gomega.ContainSubstring("STATE-CONTRACT"))
		gomega.Expect(completionBody).NotTo(gomega.ContainSubstring("EXPECTED-CONTRACT"))
	})

	ginkgo.It("selects a backend profile and inspects a fresh checkout through the config fallback", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		backendsPath := filepath.Join(workspaceRoot, ".idiom", "backends.json")
		writeDevelopmentE2EFile(t, workspaceRoot, ".idiom/backends.json", `{
  "default_profile": "local",
  "profiles": {
    "local": {"models": [{"alias": "local-coder", "kind": "llama.cpp", "port": 8080}]},
    "hosted": {"models": [{"alias": "hosted-coder", "kind": "codex", "base_url": "https://example.invalid/v1"}]}
  }
}
`)

		setResult := runDevelopmentE2EBinary(t, bin, workspaceRoot, nil, "profile", "set", "--project-root", workspaceRoot, "hosted")
		gomega.Expect(setResult.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", setResult.stdout, setResult.stderr)
		gomega.Expect(setResult.stderr).To(gomega.BeEmpty())
		gomega.Expect(setResult.stdout).To(gomega.ContainSubstring(`default profile set to "hosted"`))

		showResult := runDevelopmentE2EBinary(t, bin, workspaceRoot, nil, "profile", "show", "--project-root", workspaceRoot)
		gomega.Expect(showResult.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", showResult.stdout, showResult.stderr)
		gomega.Expect(strings.TrimSpace(showResult.stdout)).To(gomega.Equal("hosted"))

		persisted, err := backends.Load(backendsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(persisted.DefaultProfile).To(gomega.Equal("hosted"))
		gomega.Expect(persisted.Profiles).To(gomega.HaveKey("local"))
		gomega.Expect(persisted.Profiles).To(gomega.HaveKey("hosted"))

		missingConfig := filepath.Join(workspaceRoot, "not-initialized.json")
		toolResult := runDevelopmentE2EBinary(t, bin, workspaceRoot, nil,
			"tool", "pwd",
			"--config", missingConfig,
			"--profile", "discovery",
			"--caller-id", "checkout-bootstrap",
			"--request-id", "request-1",
		)
		gomega.Expect(toolResult.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", toolResult.stdout, toolResult.stderr)
		gomega.Expect(toolResult.stderr).To(gomega.BeEmpty())
		var payload struct {
			Tool    string                        `json:"Tool"`
			Output  string                        `json:"Output"`
			Error   string                        `json:"Error"`
			Sidecar workspace.ToolSidecarEnvelope `json:"sidecar"`
		}
		gomega.Expect(json.Unmarshal([]byte(toolResult.stdout), &payload)).To(gomega.Succeed(), toolResult.stdout)
		canonicalRoot := config.CanonicalizeExistingPath(workspaceRoot)
		gomega.Expect(payload.Tool).To(gomega.Equal("pwd"))
		gomega.Expect(payload.Output).To(gomega.Equal(canonicalRoot))
		gomega.Expect(payload.Error).To(gomega.BeEmpty())
		gomega.Expect(payload.Sidecar.SchemaVersion).To(gomega.Equal(workspace.ToolSidecarEnvelopeVersion))
		gomega.Expect(payload.Sidecar.Tool).To(gomega.Equal("pwd"))
		gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal(workspace.ToolProfileDiscovery))
		gomega.Expect(payload.Sidecar.Caller.CallerID).To(gomega.Equal("checkout-bootstrap"))
		gomega.Expect(payload.Sidecar.Caller.RequestID).To(gomega.Equal("request-1"))
		gomega.Expect(payload.Sidecar.Error).To(gomega.BeNil())
		gomega.Expect(fmt.Sprint(payload.Sidecar.StructuredOutput)).To(gomega.ContainSubstring(canonicalRoot))
		_, err = os.Stat(missingConfig)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "config fallback must remain read-only")
	})

	ginkgo.It("initializes a fresh repository and reports actionable implementation readiness", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()

		initResult := runDevelopmentE2EBinary(t, bin, workspaceRoot, nil, "init", workspaceRoot)
		gomega.Expect(initResult.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", initResult.stdout, initResult.stderr)
		gomega.Expect(initResult.stderr).To(gomega.BeEmpty())
		gomega.Expect(initResult.stdout).To(gomega.ContainSubstring("Initialized .idiom/"))
		for _, relativePath := range []string{
			".idiom/config.json",
			".idiom/backends.json",
			".idiom/prompts/base.md",
			".idiom/prompts/profiles/coding.md",
		} {
			info, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(relativePath)))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "missing initialized artifact %s", relativePath)
			gomega.Expect(info.Size()).To(gomega.BeNumerically(">", 0), "initialized artifact %s must not be empty", relativePath)
		}

		configPath := filepath.Join(workspaceRoot, ".idiom", "config.json")
		doctorResult := runDevelopmentE2EBinary(t, bin, workspaceRoot, map[string]string{
			"PATH":                "",
			"IDIOM_RUNNER_SOCKET": filepath.Join(workspaceRoot, "missing-runner.sock"),
		}, "doctor", "--config", configPath, "--profile", "implement", "--json")
		gomega.Expect(doctorResult.code).To(gomega.Equal(1), "stdout=%s\nstderr=%s", doctorResult.stdout, doctorResult.stderr)
		gomega.Expect(doctorResult.stderr).To(gomega.BeEmpty())

		var report struct {
			Checks []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Remedy string `json:"remedy"`
			} `json:"checks"`
			ReadinessProfiles []struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"readiness_profiles"`
		}
		gomega.Expect(json.Unmarshal([]byte(doctorResult.stdout), &report)).To(gomega.Succeed(), doctorResult.stdout)
		checks := make(map[string]struct {
			status string
			remedy string
		}, len(report.Checks))
		for _, check := range report.Checks {
			checks[check.Name] = struct {
				status string
				remedy string
			}{status: check.Status, remedy: check.Remedy}
		}
		gomega.Expect(checks).To(gomega.HaveKey("project config"))
		gomega.Expect(checks["project config"].status).To(gomega.Equal("pass"))
		gomega.Expect(checks).To(gomega.HaveKey("workspace writable"))
		gomega.Expect(checks["workspace writable"].status).To(gomega.Equal("pass"))
		gomega.Expect(checks).To(gomega.HaveKey("backend runner"))
		gomega.Expect(checks["backend runner"].status).To(gomega.Equal("fail"))
		gomega.Expect(checks["backend runner"].remedy).NotTo(gomega.BeEmpty())

		gomega.Expect(report.ReadinessProfiles).To(gomega.HaveLen(1))
		gomega.Expect(report.ReadinessProfiles[0].Name).To(gomega.Equal("implement"))
		gomega.Expect(report.ReadinessProfiles[0].State).To(gomega.Equal("blocked"))
		probes, err := filepath.Glob(filepath.Join(workspaceRoot, ".idiom-doctor-write-probe-*"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(probes).To(gomega.BeEmpty(), "doctor must clean up its writability probe")
	})

	ginkgo.It("terminates a compiled run while the backend is blocked and records one signal-owned exit", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(20*time.Second), func(ctx ginkgo.SpecContext) {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("process signals are not portable to Windows")
		}

		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		telemetryDir := t.TempDir()
		promptSecret := "PRIVATE-BLOCKED-REQUEST-9922"
		credentialSecret := "blocked-backend-key"
		requestStarted := make(chan struct{})
		releaseBackend := make(chan struct{})
		var startedOnce sync.Once
		var releaseOnce sync.Once
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			startedOnce.Do(func() { close(requestStarted) })
			<-releaseBackend
		}))
		ginkgo.DeferCleanup(server.Close)
		ginkgo.DeferCleanup(func() { releaseOnce.Do(func() { close(releaseBackend) }) })
		configPath := writeDevelopmentE2EConfig(t, workspaceRoot, server.URL, false, false)

		command := exec.Command(bin,
			"run",
			"--config", configPath,
			"--prompt", promptSecret,
			"--session", "blocked-signal",
			"--output", "json",
			"--permission-profile", "read-only",
		)
		command.Dir = rootTestRepoRoot(t)
		command.Env = rootTestEnvironmentWithOverrides(os.Environ(), map[string]string{
			"HOME":                                 t.TempDir(),
			"IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK": "1",
			"IDIOM_TELEMETRY":                      "1",
			"IDIOM_TELEMETRY_DIR":                  telemetryDir,
			developmentWorkflowAPIKeyEnv:           credentialSecret,
		})
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		gomega.Expect(command.Start()).To(gomega.Succeed())
		waitResult := make(chan error, 1)
		go func() { waitResult <- command.Wait() }()
		waited := false
		ginkgo.DeferCleanup(func() {
			if !waited {
				_ = command.Process.Kill()
				<-waitResult
			}
		})

		gomega.Eventually(ctx, requestStarted).Should(gomega.BeClosed())
		gomega.Expect(command.Process.Signal(syscall.SIGTERM)).To(gomega.Succeed())
		var waitErr error
		gomega.Eventually(ctx, waitResult).Should(gomega.Receive(&waitErr))
		waited = true
		var exitErr *exec.ExitError
		gomega.Expect(errors.As(waitErr, &exitErr)).To(gomega.BeTrue(), "stdout=%s\nstderr=%s\nerror=%v", stdout.String(), stderr.String(), waitErr)
		gomega.Expect(exitErr.ExitCode()).To(gomega.Equal(143), "stdout=%s\nstderr=%s", stdout.String(), stderr.String())

		entries, err := os.ReadDir(telemetryDir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(1))
		telemetryPath := filepath.Join(telemetryDir, entries[0].Name())
		records := readMainTelemetryRecords(t, telemetryPath)
		exits := rootProcessExitRecords(records)
		gomega.Expect(exits).To(gomega.HaveLen(1), "records=%#v", records)
		gomega.Expect(exits[0]).To(gomega.And(
			gomega.HaveKeyWithValue("exit_code", float64(143)),
			gomega.HaveKeyWithValue("reason", "signal"),
			gomega.HaveKeyWithValue("signal", syscall.SIGTERM.String()),
		))
		telemetryBody, err := os.ReadFile(telemetryPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(telemetryBody)).NotTo(gomega.ContainSubstring(promptSecret))
		gomega.Expect(string(telemetryBody)).NotTo(gomega.ContainSubstring(credentialSecret))
		releaseOnce.Do(func() { close(releaseBackend) })
	})
})
