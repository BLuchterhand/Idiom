package sweeval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/evidence/eval"
)

const scriptedModelRequestSurfaceHeader = "x-idiom-request-surface"

func sweEvalTestRepoRoot() string {

	dir, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("get cwd: %v", err))

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		gomega.Expect(parent).NotTo(gomega.Equal(dir), fmt.Sprintf("repo root not found from cwd"))

		dir = parent
	}
}

type scriptedModelProcess struct {
	baseURL  string
	command  *exec.Cmd
	stderr   *os.File
	waited   chan error
	stopOnce sync.Once
	stopErr  error
}

func startScriptedModelProcess(script string, requestLog string) *scriptedModelProcess {

	dir := ginkgo.GinkgoT().TempDir()
	scriptPath := filepath.Join(dir, "script.json")
	portFile := filepath.Join(dir, "port.txt")
	gomega.Expect(os.WriteFile(scriptPath, []byte(script), 0o644)).To(gomega.Succeed())

	stderrPath := filepath.Join(dir, "stderr.log")
	stderrFile, err := os.Create(stderrPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("create scripted model stderr log: %v", err))

	arguments := []string{
		filepath.Join(sweEvalTestRepoRoot(), "scripts", "scripted-model-server.py"),
		"--script", scriptPath,
		"--port-file", portFile,
	}
	if requestLog != "" {
		arguments = append(arguments, "--request-log", requestLog)
	}
	cmd := exec.Command("python3", arguments...)
	cmd.Dir = sweEvalTestRepoRoot()
	cmd.Stderr = stderrFile
	process := &scriptedModelProcess{
		command: cmd,
		stderr:  stderrFile,
		waited:  make(chan error, 1),
	}
	ginkgo.DeferCleanup(func() { _ = process.stop() })
	gomega.Expect(cmd.Start()).To(gomega.Succeed())
	go func() { process.waited <- cmd.Wait() }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case waitErr := <-process.waited:
			process.stopOnce.Do(func() {
				process.stopErr = waitErr
				_ = process.stderr.Close()
			})
			stderr, _ := os.ReadFile(stderrPath)
			ginkgo.Fail(fmt.Sprintf("scripted model server exited before reporting a port; stderr=%s", stderr))
		case <-time.After(25 * time.Millisecond):
			data, err := os.ReadFile(portFile)
			if err == nil && strings.TrimSpace(string(data)) != "" {
				process.baseURL = strings.TrimSpace(string(data))
			}
		}
		if process.baseURL != "" {
			break
		}
	}
	if process.baseURL == "" {
		_ = process.stop()
		stderr, _ := os.ReadFile(stderrPath)
		ginkgo.Fail(fmt.Sprintf("scripted model server did not report a port; stderr=%s", stderr))
	}

	return process
}

func (process *scriptedModelProcess) stop() error {
	process.stopOnce.Do(func() {
		if process.command.Process == nil {
			process.stopErr = process.stderr.Close()
			return
		}
		if err := process.command.Process.Signal(os.Interrupt); err != nil {
			_ = process.command.Process.Kill()
		}
		select {
		case process.stopErr = <-process.waited:
		case <-time.After(5 * time.Second):
			_ = process.command.Process.Kill()
			process.stopErr = <-process.waited
		}
		if closeErr := process.stderr.Close(); process.stopErr == nil {
			process.stopErr = closeErr
		}
	})
	return process.stopErr
}

func scriptedModelIDs(baseURL string) []string {

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/v1/models")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("get models: %v", err))

	defer resp.Body.Close()
	gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusOK), fmt.Sprintf("models status = %d, want %d", resp.StatusCode, http.StatusOK))

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	gomega.Expect(json.NewDecoder(resp.Body).Decode(&payload)).To(gomega.Succeed())

	var ids []string
	for _, model := range payload.Data {
		ids = append(ids, model.ID)
	}
	return ids
}

func scriptedModelContent(baseURL string) string {

	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/v1/chat/completions",
		strings.NewReader(`{"model":"scripted-model","messages":[{"role":"user","content":"next"}]}`),
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build chat request: %v", err))

	req.Header.Set("content-type", "application/json")

	resp, err := client.Do(req)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("post chat completion: %v", err))

	defer resp.Body.Close()
	gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusOK), fmt.Sprintf("chat status = %d, want %d", resp.StatusCode, http.StatusOK))

	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	gomega.Expect(json.NewDecoder(resp.Body).Decode(&payload)).To(gomega.Succeed())
	gomega.Expect(payload.Choices).To(gomega.HaveLen(1), fmt.Sprintf("chat choices = %#v, want one choice", payload.Choices))

	return payload.Choices[0].Message.Content
}

func postScriptedModelRequest(baseURL string, payload any, surface map[string]any) (int, map[string]any) {
	body, err := json.Marshal(payload)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal scripted model request: %v", err))
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build scripted model request: %v", err))
	req.Header.Set("content-type", "application/json")
	if surface != nil {
		surfaceBody, err := json.Marshal(surface)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal request surface: %v", err))
		req.Header.Set(scriptedModelRequestSurfaceHeader, string(surfaceBody))
	}

	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("post scripted model request: %v", err))
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted model response: %v", err))
	var decoded map[string]any
	gomega.Expect(json.Unmarshal(responseBody, &decoded)).To(gomega.Succeed(), fmt.Sprintf("decode scripted model response: %s", responseBody))
	return response.StatusCode, decoded
}

func scriptedResponseContent(payload map[string]any) string {
	choices, ok := payload["choices"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("scripted response choices = %#v", payload["choices"]))
	gomega.Expect(choices).To(gomega.HaveLen(1))
	choice, ok := choices[0].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("scripted response choice = %#v", choices[0]))
	message, ok := choice["message"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("scripted response message = %#v", choice["message"]))
	content, ok := message["content"].(string)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("scripted response content = %#v", message["content"]))
	return content
}

func readScriptedJSONL(path string) []map[string]any {
	file, err := os.Open(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open JSONL %s: %v", path, err))
	defer file.Close()

	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var record map[string]any
		gomega.Expect(json.Unmarshal(scanner.Bytes(), &record)).To(gomega.Succeed(), fmt.Sprintf("decode JSONL %s: %s", path, scanner.Text()))
		records = append(records, record)
	}
	gomega.Expect(scanner.Err()).NotTo(gomega.HaveOccurred(), fmt.Sprintf("scan JSONL %s", path))
	return records
}

func runProductionPythonScript(script string, arguments ...string) (int, string, string) {
	commandArguments := append([]string{filepath.Join(sweEvalTestRepoRoot(), "scripts", script)}, arguments...)
	command := exec.Command("python3", commandArguments...)
	command.Dir = sweEvalTestRepoRoot()
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	exitError, ok := err.(*exec.ExitError)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("run %s: %v", script, err))
	return exitError.ExitCode(), stdout.String(), stderr.String()
}

func writeJSONTestFile(path string, value any) {
	body, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal %s: %v", path, err))
	gomega.Expect(os.WriteFile(path, append(body, '\n'), 0o644)).To(gomega.Succeed())
}

var _ = ginkgo.Describe("scripted contract production Python peers", ginkgo.Label("scripted-contract-python-contract"), func() {
	ginkgo.It("selects requested tasks in order while preserving suite defaults", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		outputPath := filepath.Join(dir, "selected.json")
		writeJSONTestFile(suitePath, map[string]any{
			"name":     "canonical",
			"version":  2,
			"defaults": map[string]any{"fixture": "fixtures/repo"},
			"tasks":    []map[string]any{{"id": "read"}, {"id": "write"}},
		})

		code, stdout, stderr := runProductionPythonScript(
			"select-scripted-contract-tasks.py",
			"--suite", suitePath,
			"--output", outputPath,
			"--task", "write",
			"--task", "read",
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("selector code=%d stdout=%s stderr=%s", code, stdout, stderr))
		body, err := os.ReadFile(outputPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read selected suite: %v", err))
		var selected map[string]any
		gomega.Expect(json.Unmarshal(body, &selected)).To(gomega.Succeed())
		gomega.Expect(selected["name"]).To(gomega.Equal("canonical-selected"))
		gomega.Expect(selected["version"]).To(gomega.BeEquivalentTo(2))
		gomega.Expect(selected["coverage"]).To(gomega.Equal(map[string]any{"min_tasks": float64(2)}))
		gomega.Expect(selected["defaults"]).To(gomega.Equal(map[string]any{"fixture": "fixtures/repo"}))
		tasks, ok := selected["tasks"].([]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("selected tasks = %#v", selected["tasks"]))
		gomega.Expect(tasks).To(gomega.HaveLen(2))
		gomega.Expect(tasks[0]).To(gomega.HaveKeyWithValue("id", "write"))
		gomega.Expect(tasks[1]).To(gomega.HaveKeyWithValue("id", "read"))
	})

	ginkgo.It("preserves the canonical lane family and cross-product contract for the CI selection", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		outputPath := filepath.Join(dir, "selected.json")
		crossProducts := []map[string]any{{
			"name":                         "read-lane",
			"runner_mode":                  "auto",
			"required_tags":                []string{"read-success"},
			"required_scenario_assertions": []string{"tools"},
		}}
		writeJSONTestFile(suitePath, map[string]any{
			"name": "canonical",
			"coverage": map[string]any{
				"min_tasks":               20,
				"required_families":       []string{"information_request"},
				"required_initial_lanes":  []string{"main"},
				"required_followup_lanes": []string{"implement"},
				"required_cross_products": crossProducts,
				"required_tags":           []string{"not-needed-in-bounded-ci"},
			},
			"tasks": []map[string]any{{"id": "read"}},
		})

		code, stdout, stderr := runProductionPythonScript(
			"select-scripted-contract-tasks.py",
			"--suite", suitePath,
			"--output", outputPath,
			"--require-canonical-coverage",
			"--task", "read",
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("selector code=%d stdout=%s stderr=%s", code, stdout, stderr))
		body, err := os.ReadFile(outputPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read selected suite: %v", err))
		var selected map[string]any
		gomega.Expect(json.Unmarshal(body, &selected)).To(gomega.Succeed())
		coverage, ok := selected["coverage"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("selected coverage = %#v", selected["coverage"]))
		gomega.Expect(coverage["min_tasks"]).To(gomega.BeEquivalentTo(1))
		gomega.Expect(coverage["required_families"]).To(gomega.Equal([]any{"information_request"}))
		gomega.Expect(coverage["required_initial_lanes"]).To(gomega.Equal([]any{"main"}))
		gomega.Expect(coverage["required_followup_lanes"]).To(gomega.Equal([]any{"implement"}))
		gomega.Expect(coverage["required_cross_products"]).To(gomega.HaveLen(1))
		gomega.Expect(coverage).NotTo(gomega.HaveKey("required_tags"))
	})

	ginkgo.DescribeTable("rejects invalid requested task identities",
		func(tasks []string, want string) {
			dir := ginkgo.GinkgoT().TempDir()
			suitePath := filepath.Join(dir, "suite.json")
			outputPath := filepath.Join(dir, "selected.json")
			writeJSONTestFile(suitePath, map[string]any{
				"name":  "canonical",
				"tasks": []map[string]any{{"id": "read"}},
			})
			arguments := []string{"--suite", suitePath, "--output", outputPath}
			for _, task := range tasks {
				arguments = append(arguments, "--task", task)
			}

			code, stdout, stderr := runProductionPythonScript("select-scripted-contract-tasks.py", arguments...)
			gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("selector code=%d stdout=%s stderr=%s", code, stdout, stderr))
			gomega.Expect(stderr).To(gomega.ContainSubstring(want))
			_, err := os.Stat(outputPath)
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("invalid selection wrote output: %v", err))
		},
		ginkgo.Entry("unknown task", []string{"missing"}, "unknown scripted task ID(s): missing"),
		ginkgo.Entry("duplicate task", []string{"read", "read"}, "duplicate scripted task ID"),
	)

	ginkgo.It("records privacy-safe request surfaces and exact response consumption through the server process", func() {
		dir := ginkgo.GinkgoT().TempDir()
		requestLog := filepath.Join(dir, "requests.jsonl")
		eventLog := filepath.Join(dir, "events.jsonl")
		process := startScriptedModelProcess(`["first",{"content":"second"}]`, requestLog)

		status, first := postScriptedModelRequest(process.baseURL, map[string]any{
			"model":    "private-model-name",
			"messages": []map[string]any{{"role": "user", "content": "private prompt"}},
			"tools": []map[string]any{
				{"type": "function", "function": map[string]any{"name": "read_file"}},
				{"type": "function", "function": map[string]any{"name": "edit_file"}},
				{"type": "function", "function": map[string]any{}},
			},
			"tool_choice":     map[string]any{"type": "function", "name": "read_file"},
			"response_format": map[string]any{"private": "schema"},
		}, map[string]any{
			"schema_version":     "idiom.model_request_surface.v1",
			"transport":          "textual",
			"allowed_tool_count": 3,
			"allowed_tool_names": []string{"source_sanity", "edit_file", "read_file"},
		})
		gomega.Expect(status).To(gomega.Equal(http.StatusOK))
		gomega.Expect(scriptedResponseContent(first)).To(gomega.Equal("first"))
		status, second := postScriptedModelRequest(process.baseURL, map[string]any{
			"messages":        []any{},
			"response_format": map[string]any{"type": "json_object"},
		}, nil)
		gomega.Expect(status).To(gomega.Equal(http.StatusOK))
		gomega.Expect(scriptedResponseContent(second)).To(gomega.Equal("second"))
		gomega.Expect(process.stop()).To(gomega.Succeed())

		records := readScriptedJSONL(requestLog)
		gomega.Expect(records).To(gomega.HaveLen(3))
		request := records[0]
		gomega.Expect(request["index"]).To(gomega.BeEquivalentTo(0))
		gomega.Expect(request["kind"]).To(gomega.Equal("tool_capable"))
		gomega.Expect(request["declared_tool_count"]).To(gomega.BeEquivalentTo(3))
		gomega.Expect(request["observed_tool_count"]).To(gomega.BeEquivalentTo(2))
		gomega.Expect(request["adapted_tool_names"]).To(gomega.HaveExactElements("edit_file", "read_file"))
		gomega.Expect(request["effective_tool_names"]).To(gomega.HaveExactElements("edit_file", "read_file", "source_sanity"))
		gomega.Expect(request["transport"]).To(gomega.Equal("textual"))
		for _, privateField := range []string{"model", "messages", "tool_choice", "response_format"} {
			gomega.Expect(request).NotTo(gomega.HaveKey(privateField))
		}
		gomega.Expect(records[1]["kind"]).To(gomega.Equal("structured_text"))
		summary := records[2]
		gomega.Expect(summary["record_kind"]).To(gomega.Equal("summary"))
		gomega.Expect(summary["declared_response_count"]).To(gomega.BeEquivalentTo(2))
		gomega.Expect(summary["observed_request_count"]).To(gomega.BeEquivalentTo(2))
		gomega.Expect(summary["consumed_response_count"]).To(gomega.BeEquivalentTo(2))
		gomega.Expect(summary["fully_consumed"]).To(gomega.BeTrue())
		gomega.Expect(summary["exhaustion_count"]).To(gomega.BeEquivalentTo(0))

		code, stdout, stderr := runProductionPythonScript(
			"scripted-model-server.py",
			"--request-log", requestLog,
			"--emit-events-to", eventLog,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("event projection code=%d stdout=%s stderr=%s", code, stdout, stderr))
		events := readScriptedJSONL(eventLog)
		gomega.Expect(events).To(gomega.HaveLen(3))
		gomega.Expect(events[0]["type"]).To(gomega.Equal("idiom.scripted_model.requests"))
		gomega.Expect(events[1]["type"]).To(gomega.Equal("idiom.scripted_model.script_status"))
		gomega.Expect(events[2]["type"]).To(gomega.Equal("idiom.scripted_model.script_fully_consumed"))
		gomega.Expect(events[0]["tool_counts"]).To(gomega.HaveExactElements(float64(3), float64(0)))
		gomega.Expect(events[0]["exposed_tool_names"]).To(gomega.HaveExactElements("edit_file", "read_file", "source_sanity"))
		facts, ok := events[0]["request_facts"].([]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("request facts = %#v", events[0]["request_facts"]))
		gomega.Expect(facts).To(gomega.HaveLen(2))
		gomega.Expect(facts[0]).To(gomega.HaveKeyWithValue("kind", "tool_capable"))
		gomega.Expect(facts[1]).To(gomega.HaveKeyWithValue("kind", "structured_text"))
	})

	ginkgo.It("distinguishes incomplete and exhausted scripts in durable evaluation events", func() {
		dir := ginkgo.GinkgoT().TempDir()
		incompleteLog := filepath.Join(dir, "incomplete.jsonl")
		incompleteEvents := filepath.Join(dir, "incomplete-events.jsonl")
		incomplete := startScriptedModelProcess(`["one","two"]`, incompleteLog)
		status, _ := postScriptedModelRequest(incomplete.baseURL, map[string]any{}, nil)
		gomega.Expect(status).To(gomega.Equal(http.StatusOK))
		gomega.Expect(incomplete.stop()).To(gomega.Succeed())
		code, stdout, stderr := runProductionPythonScript(
			"scripted-model-server.py",
			"--request-log", incompleteLog,
			"--emit-events-to", incompleteEvents,
			"--require-fully-consumed",
		)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("incomplete projection code=%d stdout=%s stderr=%s", code, stdout, stderr))
		incompleteOutcome := readScriptedJSONL(incompleteEvents)[2]
		gomega.Expect(incompleteOutcome["type"]).To(gomega.Equal("idiom.scripted_model.script_incomplete"))
		gomega.Expect(incompleteOutcome["declared_response_count"]).To(gomega.BeEquivalentTo(2))
		gomega.Expect(incompleteOutcome["observed_request_count"]).To(gomega.BeEquivalentTo(1))
		gomega.Expect(incompleteOutcome["exhaustion_count"]).To(gomega.BeEquivalentTo(0))

		exhaustedLog := filepath.Join(dir, "exhausted.jsonl")
		exhaustedEvents := filepath.Join(dir, "exhausted-events.jsonl")
		exhausted := startScriptedModelProcess(`["one"]`, exhaustedLog)
		status, _ = postScriptedModelRequest(exhausted.baseURL, map[string]any{}, nil)
		gomega.Expect(status).To(gomega.Equal(http.StatusOK))
		status, response := postScriptedModelRequest(exhausted.baseURL, map[string]any{}, nil)
		gomega.Expect(status).To(gomega.Equal(http.StatusInternalServerError))
		gomega.Expect(response["error"]).To(gomega.HaveKeyWithValue("message", gomega.ContainSubstring("script exhausted")))
		gomega.Expect(exhausted.stop()).To(gomega.Succeed())
		code, stdout, stderr = runProductionPythonScript(
			"scripted-model-server.py",
			"--request-log", exhaustedLog,
			"--emit-events-to", exhaustedEvents,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("exhausted projection code=%d stdout=%s stderr=%s", code, stdout, stderr))
		exhaustedOutcome := readScriptedJSONL(exhaustedEvents)[2]
		gomega.Expect(exhaustedOutcome["type"]).To(gomega.Equal("idiom.scripted_model.script_exhausted"))
		gomega.Expect(exhaustedOutcome["declared_response_count"]).To(gomega.BeEquivalentTo(1))
		gomega.Expect(exhaustedOutcome["observed_request_count"]).To(gomega.BeEquivalentTo(2))
		gomega.Expect(exhaustedOutcome["consumed_response_count"]).To(gomega.BeEquivalentTo(1))
		gomega.Expect(exhaustedOutcome["exhaustion_count"]).To(gomega.BeEquivalentTo(1))
	})

	ginkgo.It("writes one terminal summary when the server shuts down", func() {
		dir := ginkgo.GinkgoT().TempDir()
		requestLog := filepath.Join(dir, "requests.jsonl")
		process := startScriptedModelProcess(`["done"]`, requestLog)
		status, _ := postScriptedModelRequest(process.baseURL, map[string]any{
			"messages": []any{},
			"tools": []map[string]any{{
				"type":     "function",
				"function": map[string]any{"name": "source_sanity"},
			}},
		}, nil)
		gomega.Expect(status).To(gomega.Equal(http.StatusOK))
		gomega.Expect(process.stop()).To(gomega.Succeed())
		records := readScriptedJSONL(requestLog)
		gomega.Expect(records).To(gomega.HaveLen(2))
		gomega.Expect(records[1]["record_kind"]).To(gomega.Equal("summary"))
		gomega.Expect(records[1]["fully_consumed"]).To(gomega.BeTrue())
	})
})

var _ = ginkgo.Describe("swe eval", func() {
	ginkgo.It("scripted model server defaults missing model", func() {
		process := startScriptedModelProcess(`{"responses":[{"content":"ok"}]}`, "")
		if got := scriptedModelIDs(process.baseURL); !slices.Equal(got, []string{"scripted-model"}) {
			ginkgo.Fail(fmt.Sprintf("models response = %#v, want scripted-model default", got))
		}
	})
	ginkgo.DescribeTable("accepts compact scripted response formats",
		func(script, want string) {
			process := startScriptedModelProcess(script, "")
			gomega.Expect(scriptedModelContent(process.baseURL)).To(gomega.Equal(want))
		},
		ginkgo.Entry("legacy object envelope", `{"responses":[{"content":"legacy"}]}`, "legacy"),
		ginkgo.Entry("top-level array of objects", `[{"content":"array object"}]`, "array object"),
		ginkgo.Entry("wrapped string response", `{"responses":["wrapped string"]}`, "wrapped string"),
		ginkgo.Entry("top-level array of strings", `["array string"]`, "array string"),
	)
	ginkgo.It("scripted contract response scripts remain content only", func() {
		paths, err := filepath.Glob(filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "scripts", "*.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("glob scripted response scripts: %v", err))
		gomega.Expect(paths).NotTo(gomega.BeEmpty(), "no scripted response scripts found")

		for _, path := range paths {
			data, err := os.ReadFile(path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))

			var script any
			gomega.Expect(json.Unmarshal(data, &script)).To(gomega.Succeed())

			var responses []any
			switch value := script.(type) {
			case []any:
				responses = value
			case map[string]any:
				if len(value) != 1 {
					ginkgo.Fail(fmt.Sprintf("%s has top-level keys beyond responses: %#v", path, value))
				}
				rawResponses, ok := value["responses"].([]any)
				if !ok {
					ginkgo.Fail(fmt.Sprintf("%s responses must be an array, got %#v", path, value["responses"]))
				}
				responses = rawResponses
			default:
				ginkgo.Fail(fmt.Sprintf("%s must be a response array or object envelope, got %#v", path, script))
			}

			for i, response := range responses {
				switch value := response.(type) {
				case string:
					continue
				case map[string]any:
					content, ok := value["content"].(string)
					if !ok || len(value) != 1 || content == "" {
						ginkgo.Fail(fmt.Sprintf("%s response %d must contain only non-empty content, got %#v", path, i, value))
					}
				default:
					ginkgo.Fail(fmt.Sprintf("%s response %d must be content string or object, got %#v", path, i, response))
				}
			}
		}
	})
	ginkgo.It("live contract runner writes turns completely to pty", func() {
		scriptPath := filepath.Join(sweEvalTestRepoRoot(), "scripts", "live-contract-runner.sh")
		body, err := os.ReadFile(scriptPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read live contract runner: %v", err))

		text := string(body)
		for _, want := range []string{
			"def write_all(fd, data):",
			`write_all(master, turn.encode("utf-8") + b"\r")`,
			`write_all(master, b"\x03")`,
		} {
			gomega.Expect(text).To(gomega.ContainSubstring(want), fmt.Sprintf("live contract runner must use complete PTY writes; missing %q", want))

		}
		gomega.Expect(text).NotTo(gomega.ContainSubstring(`os.write(master, turn.encode("utf-8") + b"\r")`), "live contract runner must not use single os.write calls for PTY input")
		gomega.Expect(text).NotTo(gomega.ContainSubstring(`os.write(master, b"\x03")`), "live contract runner must not use single os.write calls for PTY input")

	})
	ginkgo.It("live contract runner waits for typed terminal status after a multi-turn planning question", ginkgo.Label("scripted-contract-python-contract", "scripted-contract-runner-contract"), func() {
		if _, err := exec.LookPath("bash"); err != nil {
			ginkgo.Skip(fmt.Sprintf("bash not available: %v", err))
		}

		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip(fmt.Sprintf("git not available: %v", err))
		}

		dir := ginkgo.GinkgoT().TempDir()
		configPath := filepath.Join(dir, "config.json")
		logPath := filepath.Join(dir, "contract.jsonl")
		fakeBin := filepath.Join(dir, "fake-idiom")
		gomega.Expect(os.WriteFile(configPath, []byte(`{"chat":{"system_prompt_files":[]},"workspace":{},"llama_server":{},"implementer":{},"route_classifier":{}}`), 0o644)).To(gomega.Succeed())

		fake := `#!/usr/bin/env python3
import json
import pathlib
import sys
import time

args = sys.argv[1:]
config_path = args[args.index("-config") + 1]
cfg = json.load(open(config_path, encoding="utf-8"))
debug = pathlib.Path(cfg["chat"]["debug_log_dir"])
debug.mkdir(parents=True, exist_ok=True)
log_path = debug / "fake-tui-session.jsonl"

def emit(record):
    with log_path.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(record, sort_keys=True) + "\n")

first_turn = sys.stdin.readline().strip()
emit({"type": "message", "role": "user", "content": first_turn})
emit({"type": "idiom.feature_planner.complete", "status": "planning-question-open"})
print("feature-planner: planning-question-open", flush=True)

second_turn = sys.stdin.readline().strip()
emit({"type": "message", "role": "user", "content": second_turn})
emit({"type": "message", "role": "assistant", "content": "second-turn-observed"})
print("second-turn-observed", flush=True)
time.sleep(0.5)
emit({"type": "idiom.feature_planner.complete", "status": "complete"})
print("feature-planner: complete", flush=True)
time.sleep(30)
`
		gomega.Expect(os.WriteFile(fakeBin, []byte(fake), 0o755)).To(gomega.Succeed())

		scriptPath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(), "scripts", "live-contract-runner.sh"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("script path: %v", err))

		fixturePath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(), "testdata", "live-contracts", "fixtures", "frozen-label-repo"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("fixture path: %v", err))

		turnsJSON, err := json.Marshal([]string{
			"/feature-planner Ask a planning question before completion.",
			"Approved; continue to the terminal result.",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		cmd := exec.Command("bash", scriptPath)
		cmd.Dir = fixturePath
		cmd.Env = append(os.Environ(),
			"IDIOM_BIN="+fakeBin,
			"IDIOM_CONFIG="+configPath,
			"IDIOM_SWE_FIXTURE="+fixturePath,
			"IDIOM_SWE_LOG="+logPath,
			"IDIOM_SWE_TURNS_JSON="+string(turnsJSON),
			"IDIOM_SWE_RUNNER_MODE=tui",
			"IDIOM_SWE_FAMILY=feature_planner",
			"IDIOM_SWE_TASK_TIMEOUT=15",
		)

		output, err := cmd.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("multi-turn TUI live contract runner failed: %v\n%s", err, output))

		records, err := eval.LoadRecords(logPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load multi-turn TUI records: %v", err))

		questionIndex := -1
		secondTurnIndex := -1
		terminalIndex := -1
		for i, record := range records {
			switch {
			case record.Type == "idiom.feature_planner.complete" && fmt.Sprint(record.Data["status"]) == "planning-question-open":
				questionIndex = i
			case record.Type == "message" && fmt.Sprint(record.Data["content"]) == "second-turn-observed":
				secondTurnIndex = i
			case record.Type == "idiom.feature_planner.complete" && fmt.Sprint(record.Data["status"]) == "complete":
				terminalIndex = i
			}
		}

		gomega.Expect(questionIndex).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("expected planning-question-open event, records=%#v", records))
		gomega.Expect(secondTurnIndex).To(gomega.BeNumerically(">", questionIndex), fmt.Sprintf("runner stopped before the second turn was observed, records=%#v", records))
		gomega.Expect(terminalIndex).To(gomega.BeNumerically(">", secondTurnIndex), fmt.Sprintf("runner accepted a nonterminal question event as final completion, records=%#v", records))
	})
	ginkgo.It("run suite only passes valid suite", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		writeSWESmokeSuite(suitePath, "")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("PASS cli-smoke-suite"), fmt.Sprintf("expected passing suite report, got %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Lint: PASS"), fmt.Sprintf("expected passing suite report, got %s", stdout.String()))

	})
	ginkgo.It("run evaluates logs directory", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		logsDir := filepath.Join(dir, "logs")
		gomega.Expect(os.MkdirAll(logsDir, 0o755)).To(gomega.Succeed())

		writeSWESmokeSuite(suitePath, "")
		writeSWESmokeLog(filepath.Join(logsDir, "direct.jsonl"))

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-logs", logsDir}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		for _, want := range []string{"PASS cli-smoke-suite", "Results: passed=1 failed=0", "Control rails dashboard:", "- PASS direct"} {
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("expected suite output to contain %q, got %s", want, stdout.String()))

		}
	})
	ginkgo.It("run live generates logs and scores them", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		logsDir := filepath.Join(dir, "generated")

		writeSWESmokeSuite(suitePath, "")

		command := strings.Join([]string{
			`printf '%s\n'`,
			`'{"type":"message","role":"user","content":"Explain routing."}'`,
			`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
			`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
			`> {log_path}`,
		}, " ")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		if _, err := os.Stat(filepath.Join(logsDir, "direct.jsonl")); err != nil {
			ginkgo.Fail(fmt.Sprintf("expected generated log: %v", err))
		}
	})
	ginkgo.It("run live passes multi turn metadata", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		logsDir := filepath.Join(dir, "generated")

		writeSWEMultiTurnSmokeSuite(suitePath)

		command := strings.Join([]string{
			`test "$IDIOM_SWE_TURN_COUNT" = "2"`,
			`&& test "$IDIOM_SWE_FAMILY" = "direct_question"`,
			`&& test "$IDIOM_SWE_TURNS_JSON" = '["first turn","second turn"]'`,
			`&& printf '%s\n'`,
			`'{"type":"message","role":"user","content":"first turn"}'`,
			`'{"type":"message","role":"assistant","content":"first answer"}'`,
			`'{"type":"message","role":"user","content":"second turn"}'`,
			`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
			`'{"type":"message","role":"assistant","content":"second answer"}'`,
			`> {log_path}`,
		}, " ")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("PASS cli-smoke-suite"), fmt.Sprintf("expected passing suite, got stdout=%s stderr=%s", stdout.String(), stderr.String()))

	})
	ginkgo.It("run live passes runner mode metadata", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		logsDir := filepath.Join(dir, "generated")
		suite := eval.SWESuite{
			Name: "cli-runner-mode-suite",
			Tasks: []eval.SWETask{{
				ID:         "scripted",
				Family:     "implementation",
				RunnerMode: "implement",
				Prompt:     "Implement routing.",
				Tags:       []string{"implement"},
				Scenario: eval.Scenario{
					Name:                "scripted",
					PromptContains:      []string{"Implement routing"},
					RequireChangedFiles: []string{"README.md"},
					RequireVerification: "none",
				},
				Oracle: eval.SWEOracle{
					RequireChangedFiles: []string{"README.md"},
				},
			}},
		}
		gomega.Expect(writeSWESuite(suitePath, suite)).To(gomega.Succeed())

		command := strings.Join([]string{
			`test "$IDIOM_SWE_RUNNER_MODE" = "implement"`,
			`&& printf '%s\n'`,
			`'{"type":"message","role":"user","content":"Implement routing."}'`,
			`'{"type":"tool_request","tool":"edit_file","path":"README.md"}'`,
			`'{"type":"tool_result","tool":"edit_file","path":"README.md","summary":"Edited README.md"}'`,
			`'{"type":"message","role":"assistant","content":"Changed files:\n- README.md"}'`,
			`> {log_path}`,
		}, " ")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

	})
	ginkgo.It("run live continue on error runs remaining tasks and writes jsonl failure", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		logsDir := filepath.Join(dir, "generated")

		writeSWETwoTaskSmokeSuite(suitePath)

		command := strings.Join([]string{
			`if test "$IDIOM_SWE_TASK_ID" = "first"; then`,
			`printf 'not json\n' > {log_path}; exit 7;`,
			`else`,
			`printf '%s\n'`,
			`'{"type":"message","role":"user","content":"Explain routing."}'`,
			`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
			`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
			`> {log_path};`,
			`fi`,
		}, " ")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-run-live", "-run-live-continue-on-error", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected failing suite exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("failed first"), fmt.Sprintf("expected continue-on-error progress, got stderr=%s", stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("[2/2] finished second"), fmt.Sprintf("expected continue-on-error progress, got stderr=%s", stderr.String()))

		firstLog, err := os.ReadFile(filepath.Join(logsDir, "first.jsonl"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read first log: %v", err))
		gomega.Expect(string(firstLog)).To(gomega.ContainSubstring(`"type":"error"`), fmt.Sprintf("expected synthetic JSONL error log, got %s", firstLog))
		gomega.Expect(string(firstLog)).To(gomega.ContainSubstring("live runner task failed"), fmt.Sprintf("expected synthetic JSONL error log, got %s", firstLog))

		if _, err := eval.LoadRecords(filepath.Join(logsDir, "first.jsonl")); err != nil {
			ginkgo.Fail(fmt.Sprintf("synthetic error log must be loadable JSONL: %v", err))
		}

		if _, err := os.Stat(filepath.Join(logsDir, "second.jsonl")); err != nil {
			ginkgo.Fail(fmt.Sprintf("expected second task log after first failure: %v", err))
		}
	})
	ginkgo.It("run live scores nonzero runner when log is loadable", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		logsDir := filepath.Join(dir, "generated")

		writeSWESmokeSuite(suitePath, "")

		command := strings.Join([]string{
			`printf '%s\n'`,
			`'{"type":"message","role":"user","content":"Explain routing."}'`,
			`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
			`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
			`> {log_path};`,
			`exit 1`,
		}, " ")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected scoreable nonzero runner log to pass, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("runner exited nonzero"), fmt.Sprintf("expected nonzero runner notice, got stderr=%s", stderr.String()))

	})
	ginkgo.It("live contract runner rolls back workspace with fake binary", func() {
		if _, err := exec.LookPath("bash"); err != nil {
			ginkgo.Skip(fmt.Sprintf("bash not available: %v", err))
		}

		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip(fmt.Sprintf("git not available: %v", err))
		}

		dir := ginkgo.GinkgoT().TempDir()
		configPath := filepath.Join(dir, "config.json")
		logPath := filepath.Join(dir, "contract.jsonl")
		fakeBin := filepath.Join(dir, "fake-idiom")
		gomega.Expect(os.WriteFile(configPath, []byte(`{"chat":{"system_prompt_files":[]},"workspace":{},"llama_server":{},"implementer":{},"route_classifier":{}}`), 0o644)).To(gomega.Succeed())

		fake := `#!/usr/bin/env bash
set -euo pipefail
config=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -config) config="$2"; shift 2 ;;
    *) shift ;;
  esac
done
python3 - "$config" <<'PY'
import json
import os
import pathlib
import sys
cfg = json.load(open(sys.argv[1], encoding="utf-8"))
root = pathlib.Path(cfg["workspace"]["root"])
debug = pathlib.Path(cfg["chat"]["debug_log_dir"])
debug.mkdir(parents=True, exist_ok=True)
pathlib.Path("logs/telemetry").mkdir(parents=True, exist_ok=True)
pathlib.Path("logs/telemetry/cwd-leak.jsonl").write_text("{}\n", encoding="utf-8")
(root / "internal/labels/labels.go").write_text('package labels\n\nfunc ShortLabel() string {\n\treturn "Save"\n}\n', encoding="utf-8")
with (debug / "fake-session.jsonl").open("w", encoding="utf-8") as f:
    f.write(json.dumps({"type":"session_start","workspace_root":str(root)}) + "\n")
    f.write(json.dumps({"type":"message","role":"user","content":"Implement this feature"}) + "\n")
    f.write(json.dumps({"type":"tool_request","step":1,"tool":"edit_file"}) + "\n")
    f.write(json.dumps({"type":"tool_result","step":1,"tool":"edit_file","summary":"Edited internal/labels/labels.go","error":""}) + "\n")
    f.write(json.dumps({"type":"phase","lane":"implement","phase":"verify","state":"done"}) + "\n")
    f.write(json.dumps({"type":"message","role":"assistant","content":"Done."}) + "\n")
PY
`
		gomega.Expect(os.WriteFile(fakeBin, []byte(fake), 0o755)).To(gomega.Succeed())

		scriptPath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(), "scripts", "live-contract-runner.sh"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("script path: %v", err))

		fixturePath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(), "testdata", "live-contracts", "fixtures", "frozen-label-repo"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("fixture path: %v", err))

		fixtureLeak := filepath.Join(fixturePath, "logs", "telemetry", "cwd-leak.jsonl")

		_ = os.Remove(fixtureLeak)

		cmd := exec.Command("bash", scriptPath)

		cmd.Dir = fixturePath
		cmd.Env = append(os.Environ(),
			"IDIOM_BIN="+fakeBin,
			"IDIOM_CONFIG="+configPath,
			"IDIOM_SWE_FIXTURE="+fixturePath,
			"IDIOM_SWE_LOG="+logPath,
			"IDIOM_SWE_PROMPT=Implement this feature",
			"IDIOM_SWE_TASK_TIMEOUT=15",
		)

		output, err := cmd.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("live contract runner failed: %v\n%s", err, output))

		data, err := os.ReadFile(logPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read log: %v", err))

		for _, want := range []string{
			`"type": "idiom.live_contract.workspace_prepared"`,
			`"type": "idiom.live_contract.rollback"`,
			`"status": "clean"`,
			`"changed_before_reset": ["internal/labels/labels.go"]`,
			`"changed_after_reset": []`,
		} {
			gomega.Expect(string(data)).To(gomega.ContainSubstring(want), fmt.Sprintf("expected log to contain %q, got:\n%s", want, data))

		}

		records, err := eval.LoadRecords(logPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load records: %v", err))

		metrics := eval.Analyze(records)
		foundChanged := false

		for _, path := range metrics.ChangedFiles {
			if strings.EqualFold(strings.TrimSpace(path), "internal/labels/labels.go") {
				foundChanged = true
				break
			}
		}
		gomega.Expect(foundChanged).To(gomega.BeTrue(), fmt.Sprintf("expected rollback changed-file evidence, got %#v", metrics.ChangedFiles))

		if _, err := os.Stat(fixtureLeak); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("live runner leaked cwd telemetry into fixture source: %v", err))
		}
	})
	ginkgo.It("live contract runner skips stale write when log ownership changes", func() {
		if _, err := exec.LookPath("bash"); err != nil {
			ginkgo.Skip(fmt.Sprintf("bash not available: %v", err))
		}

		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip(fmt.Sprintf("git not available: %v", err))
		}

		dir := ginkgo.GinkgoT().TempDir()
		configPath := filepath.Join(dir, "config.json")
		logPath := filepath.Join(dir, "contract.jsonl")
		fakeBin := filepath.Join(dir, "fake-idiom")
		gomega.Expect(os.WriteFile(configPath, []byte(`{"chat":{"system_prompt_files":[]},"workspace":{},"llama_server":{},"implementer":{},"route_classifier":{}}`), 0o644)).To(gomega.Succeed())

		fake := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '{"type":"message","role":"user","content":"newer run owns this log"}' > "$IDIOM_SWE_LOG"
printf '%s\n' 'newer-run' > "$IDIOM_SWE_LOG.owner"
config=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -config) config="$2"; shift 2 ;;
    *) shift ;;
  esac
done
python3 - "$config" <<'PY'
import json
import pathlib
import sys
cfg = json.load(open(sys.argv[1], encoding="utf-8"))
debug = pathlib.Path(cfg["chat"]["debug_log_dir"])
debug.mkdir(parents=True, exist_ok=True)
with (debug / "stale-session.jsonl").open("w", encoding="utf-8") as f:
    f.write('{"type":"message","role":"user","content":"stale run"}\n')
PY
`
		gomega.Expect(os.WriteFile(fakeBin, []byte(fake), 0o755)).To(gomega.Succeed())

		scriptPath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(), "scripts", "live-contract-runner.sh"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("script path: %v", err))

		fixturePath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(), "testdata", "live-contracts", "fixtures", "frozen-label-repo"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("fixture path: %v", err))

		cmd := exec.Command("bash", scriptPath)

		cmd.Dir = fixturePath
		cmd.Env = append(os.Environ(),
			"IDIOM_BIN="+fakeBin,
			"IDIOM_CONFIG="+configPath,
			"IDIOM_SWE_FIXTURE="+fixturePath,
			"IDIOM_SWE_LOG="+logPath,
			"IDIOM_SWE_PROMPT=Implement this feature",
			"IDIOM_SWE_TASK_TIMEOUT=15",
		)

		output, err := cmd.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("live contract runner failed: %v\n%s", err, output))

		data, err := os.ReadFile(logPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read log: %v", err))
		gomega.Expect(string(data)).To(gomega.ContainSubstring("newer run owns this log"), fmt.Sprintf("expected newer owner log to remain, got:\n%s", data))

		for _, stale := range []string{"stale run", "idiom.live_contract.rollback", "idiom.live_contract.workspace_prepared"} {
			gomega.Expect(string(data)).NotTo(gomega.ContainSubstring(stale), fmt.Sprintf("stale runner wrote %q into newer log:\n%s", stale, data))

		}
	})
	ginkgo.It("live contract runner archives git ref fixture", func() {
		if _, err := exec.LookPath("bash"); err != nil {
			ginkgo.Skip(fmt.Sprintf("bash not available: %v", err))
		}

		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip(fmt.Sprintf("git not available: %v", err))
		}

		dir := ginkgo.GinkgoT().TempDir()
		sourceRepo := filepath.Join(dir, "source")
		gomega.Expect(os.MkdirAll(filepath.Join(sourceRepo, "internal", "labels"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(sourceRepo, "internal", "labels", "labels.go"), []byte("package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"), 0o644)).To(gomega.Succeed())

		runGit := func(args ...string) {

			cmd := exec.Command("git", append([]string{"-C", sourceRepo}, args...)...)
			if output, err := cmd.CombinedOutput(); err != nil {
				ginkgo.Fail(fmt.Sprintf("git %v failed: %v\n%s", args, err, output))
			}
		}
		runGit("init", "-q")
		runGit("config", "user.email", "live-contracts@example.invalid")
		runGit("config", "user.name", "Idiom Live Contracts")
		runGit("add", "-A")
		runGit("commit", "-q", "-m", "frozen source")

		commit := strings.TrimSpace(string(mustCommandOutput("git", "-C", sourceRepo, "rev-parse", "HEAD")))
		gomega.Expect(os.WriteFile(filepath.Join(sourceRepo, "dirty-untracked.txt"), []byte("must not enter archived fixture\n"), 0o644)).To(gomega.Succeed())

		configPath := filepath.Join(dir, "config.json")
		logPath := filepath.Join(dir, "contract.jsonl")
		fakeBin := filepath.Join(dir, "fake-idiom")
		gomega.Expect(os.WriteFile(configPath, []byte(`{"chat":{"system_prompt_files":[]},"workspace":{},"llama_server":{},"implementer":{},"route_classifier":{}}`), 0o644)).To(gomega.Succeed())

		fake := `#!/usr/bin/env bash
set -euo pipefail
config=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -config) config="$2"; shift 2 ;;
    *) shift ;;
  esac
done
python3 - "$config" <<'PY'
import json
import pathlib
import sys
cfg = json.load(open(sys.argv[1], encoding="utf-8"))
root = pathlib.Path(cfg["workspace"]["root"])
debug = pathlib.Path(cfg["chat"]["debug_log_dir"])
debug.mkdir(parents=True, exist_ok=True)
if (root / "dirty-untracked.txt").exists():
    raise SystemExit("untracked source file leaked into archived fixture")
(root / "internal/labels/labels.go").write_text('package labels\n\nfunc ShortLabel() string {\n\treturn "Save"\n}\n', encoding="utf-8")
with (debug / "fake-session.jsonl").open("w", encoding="utf-8") as f:
    f.write(json.dumps({"type":"session_start","workspace_root":str(root)}) + "\n")
    f.write(json.dumps({"type":"message","role":"user","content":"Implement this feature"}) + "\n")
    f.write(json.dumps({"type":"phase","lane":"implement","phase":"verify","state":"done"}) + "\n")
    f.write(json.dumps({"type":"message","role":"assistant","content":"Done."}) + "\n")
PY
`
		gomega.Expect(os.WriteFile(fakeBin, []byte(fake), 0o755)).To(gomega.Succeed())

		scriptPath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(), "scripts", "live-contract-runner.sh"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("script path: %v", err))

		cmd := exec.Command("bash", scriptPath)

		cmd.Dir = sourceRepo
		cmd.Env = append(os.Environ(),
			"IDIOM_BIN="+fakeBin,
			"IDIOM_CONFIG="+configPath,
			"IDIOM_SWE_FIXTURE_GIT_REPO="+sourceRepo,
			"IDIOM_SWE_FIXTURE_GIT_REF="+commit,
			"IDIOM_SWE_LOG="+logPath,
			"IDIOM_SWE_PROMPT=Implement this feature",
			"IDIOM_SWE_TASK_TIMEOUT=15",
		)

		output, err := cmd.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("live contract git-ref runner failed: %v\n%s", err, output))

		data, err := os.ReadFile(logPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read log: %v", err))

		for _, want := range []string{
			`"fixture_source_kind": "git_ref"`,
			`"fixture_git_ref": "` + commit + `"`,
			`"source_commit": "` + commit + `"`,
			`"changed_before_reset": ["internal/labels/labels.go"]`,
			`"status": "clean"`,
		} {
			gomega.Expect(string(data)).To(gomega.ContainSubstring(want), fmt.Sprintf("expected log to contain %q, got:\n%s", want, data))

		}
	})
	ginkgo.It("run live derives task timeout from oracle and reports progress", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		logsDir := filepath.Join(dir, "generated")
		maxDurationMillis := int64(45000)
		requireApproval := false
		suite := eval.SWESuite{
			Name: "cli-timeout-suite",
			Tasks: []eval.SWETask{{
				ID:     "direct",
				Family: "direct_question",
				Prompt: "Explain routing.",
				Tags:   []string{"read-only"},
				Scenario: eval.Scenario{
					Name:                "direct",
					PromptContains:      []string{"Explain routing"},
					RequireVerification: "none",
					RequireApproval:     &requireApproval,
					RequireRoutePlan: &eval.RoutePlanExpectation{
						InitialLane: "main",
						Reason:      "direct_conversation",
						Contract:    "direct_answer",
					},
				},
				Oracle: eval.SWEOracle{
					RequireNoChangedFiles: true,
					RequireNoApproval:     true,
					MaxDurationMillis:     &maxDurationMillis,
				},
			}},
		}
		gomega.Expect(writeSWESuite(suitePath, suite)).To(gomega.Succeed())

		command := strings.Join([]string{
			`test "$IDIOM_SWE_TASK_TIMEOUT" = "75"`,
			`&& printf '%s\n'`,
			`'{"type":"message","role":"user","content":"Explain routing."}'`,
			`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
			`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
			`> {log_path}`,
		}, " ")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("[1/1] running direct (timeout=75s)"), fmt.Sprintf("expected task start progress in stderr, got %s", stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("[1/1] finished direct in "), fmt.Sprintf("expected task finish progress in stderr, got %s", stderr.String()))

	})
	ginkgo.It("task env entries filter runner owned env", func() {
		entries := taskEnvEntries(map[string]string{
			"IDIOM_SWE_LOG":    "/tmp/should-not-win.jsonl",
			"IDIOM_BIN":        "/tmp/idiom",
			"CUSTOM_ENV":       "visible",
			" IDIOM_SWE_TASK ": "ignored",
		})
		joined := strings.Join(entries, "\n")
		gomega.Expect(joined).NotTo(gomega.ContainSubstring("IDIOM_SWE_LOG="), fmt.Sprintf("runner-owned env keys must be filtered, got %#v", entries))
		gomega.Expect(joined).NotTo(gomega.ContainSubstring("IDIOM_BIN="), fmt.Sprintf("runner-owned env keys must be filtered, got %#v", entries))
		gomega.Expect(joined).NotTo(gomega.ContainSubstring("IDIOM_SWE_TASK="), fmt.Sprintf("runner-owned env keys must be filtered, got %#v", entries))
		gomega.Expect(joined).To(gomega.ContainSubstring("CUSTOM_ENV=visible"), fmt.Sprintf("custom env should remain, got %#v", entries))

	})
	ginkgo.It("live task timeout allows long plan execute atoms", func() {
		maxDurationMillis := int64(900000)
		got := liveTaskTimeoutSeconds(eval.SWETask{
			ID:     "medium-plan-execute",
			Family: "plan_then_execute_planner",
			Oracle: eval.SWEOracle{MaxDurationMillis: &maxDurationMillis},
		})
		gomega.Expect(got).To(gomega.Equal(930), fmt.Sprintf("timeout = %d, want 930", got))

	})
	ginkgo.It("run writes failing scenario suite", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		logsDir := filepath.Join(dir, "logs")
		outPath := filepath.Join(dir, "regressions.json")
		gomega.Expect(os.MkdirAll(logsDir, 0o755)).To(gomega.Succeed())

		writeSWESmokeSuite(suitePath, "")
		gomega.Expect(os.WriteFile(filepath.Join(logsDir, "direct.jsonl"), []byte(`{"type":"message","role":"user","content":"Explain routing."}
{"type":"message","role":"assistant","content":"No route plan here."}
`), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-logs", logsDir, "-write-failing-scenarios", outPath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected failing eval exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		data, err := os.ReadFile(outPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read generated suite: %v", err))
		gomega.Expect(string(data)).To(gomega.ContainSubstring("direct-regression"), fmt.Sprintf("expected generated regression task, got %s", data))
		gomega.Expect(string(data)).To(gomega.ContainSubstring("generated-regression"), fmt.Sprintf("expected generated regression task, got %s", data))

	})
	ginkgo.It("render runner command does not replace placeholders inside values", func() {
		rendered := renderRunnerCommand(`printf '%s %s\n' {prompt} {log_path}`, map[string]string{
			"prompt":   "literal {log_path} in prompt",
			"log_path": "/tmp/out.jsonl",
		})
		gomega.Expect(rendered).To(gomega.ContainSubstring(`'literal {log_path} in prompt'`), fmt.Sprintf("expected prompt placeholder text to remain literal, got %s", rendered))
		gomega.Expect(rendered).To(gomega.ContainSubstring(`'/tmp/out.jsonl'`), fmt.Sprintf("expected log path replacement, got %s", rendered))

		rendered = renderRunnerCommand(`printf '%s\n' {turns_json}`, map[string]string{
			"turns_json": `["first turn","second turn"]`,
		})
		gomega.Expect(rendered).To(gomega.ContainSubstring(`'["first turn","second turn"]'`), fmt.Sprintf("expected turns_json replacement, got %s", rendered))

	})
	ginkgo.It("run reports missing logs", func() {
		dir := ginkgo.GinkgoT().TempDir()
		suitePath := filepath.Join(dir, "suite.json")
		writeSWESmokeSuite(suitePath, "")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected exit 1 for missing logs, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("missing_logs=1"), fmt.Sprintf("expected missing log summary, got %s", stdout.String()))

	})
	ginkgo.It("task log path uses explicit and logs dir paths", func() {
		task := smokeSWETask("")
		if got := taskLogPath("/tmp/suite.json", "/tmp/logs", task); got != filepath.Join("/tmp/logs", "direct.jsonl") {
			ginkgo.Fail(fmt.Sprintf("unexpected logs-dir path: %q", got))
		}

		task.LogPath = "custom/direct.jsonl"
		if got := taskLogPath("/tmp/suites/suite.json", "/tmp/logs", task); got != filepath.Clean("/tmp/suites/custom/direct.jsonl") {
			ginkgo.Fail(fmt.Sprintf("unexpected relative explicit path: %q", got))
		}

		task.LogPath = filepath.Join(string(filepath.Separator), "var", "tmp", "direct.jsonl")
		if got := taskLogPath("/tmp/suites/suite.json", "/tmp/logs", task); got != filepath.Clean(task.LogPath) {
			ginkgo.Fail(fmt.Sprintf("unexpected absolute explicit path: %q", got))
		}
	})
	ginkgo.It("representative suite lints clean", func() {

		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "swe", "representative-suite.json")
		if _, err := os.Stat(suitePath); err != nil {
			ginkgo.Fail(fmt.Sprintf("representative suite missing — drift sentinel cannot run: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("representative suite failed -suite-only lint (exit %d).\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Lint: PASS"), fmt.Sprintf("expected `Lint: PASS` in suite report, got:\n%s", stdout.String()))

	})
	ginkgo.It("cli parity regression suite lints clean", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "swe", "cli-parity-regression-suite.json")
		if _, err := os.Stat(suitePath); err != nil {
			ginkgo.Fail(fmt.Sprintf("cli parity regression suite missing: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("cli parity regression suite failed -suite-only lint (exit %d).\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Lint: PASS"), fmt.Sprintf("expected `Lint: PASS` in suite report, got:\n%s", stdout.String()))

	})
	ginkgo.It("live contract suite lints clean", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "live-contracts", "suite.json")
		if _, err := os.Stat(suitePath); err != nil {
			ginkgo.Fail(fmt.Sprintf("live contract suite missing: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("live contract suite failed -suite-only lint (exit %d).\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Lint: PASS"), fmt.Sprintf("expected `Lint: PASS` in suite report, got:\n%s", stdout.String()))

	})
	ginkgo.It("scripted contract suite lints clean", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")
		if _, err := os.Stat(suitePath); err != nil {
			ginkgo.Fail(fmt.Sprintf("scripted contract suite missing: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("scripted contract suite failed -suite-only lint (exit %d).\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Lint: PASS"), fmt.Sprintf("expected `Lint: PASS` in suite report, got:\n%s", stdout.String()))

	})
	ginkgo.It("scripted contract suite defaults expand pinned task semantics", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")

		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		taskByID := map[string]eval.SWETask{}
		for _, task := range suite.Tasks {
			gomega.Expect(task.ID).NotTo(gomega.BeEmpty(), "scripted contract task IDs must be explicit")
			_, duplicate := taskByID[task.ID]
			gomega.Expect(duplicate).To(gomega.BeFalse(), fmt.Sprintf("duplicate scripted contract task ID %q", task.ID))
			taskByID[task.ID] = task
		}
		gomega.Expect(taskByID).To(gomega.HaveLen(len(suite.Tasks)), "every scripted contract task must have one unique ID")

		scriptPaths, err := filepath.Glob(filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "scripts", "*.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("glob scripted response scripts: %v", err))

		scripts := map[string]bool{}
		for _, path := range scriptPaths {
			scripts[strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))] = true
		}

		for _, task := range suite.Tasks {
			gomega.Expect(task.Fixture).NotTo(gomega.Equal(""), fmt.Sprintf("%s fixture must be explicit or expanded from defaults", task.ID))

			assertTaskHasTags(task, "deterministic", "fake-model")
			assertTaskHasRunEvents(task,
				"idiom.live_contract.rollback",
				"idiom.scripted_model.script_fully_consumed",
			)
			gomega.Expect(scripts[task.ID]).To(gomega.BeTrue(), fmt.Sprintf("%s missing scripted response file", task.ID))

		}

		extraScripts := []string{}
		for id := range scripts {
			if _, ok := taskByID[id]; !ok {
				extraScripts = append(extraScripts, id)
			}
		}
		slices.Sort(extraScripts)
		gomega.Expect(slices.Equal(extraScripts, []string{})).To(gomega.BeTrue(), fmt.Sprintf("unexpected scripted response files without suite tasks: %#v", extraScripts))

		for _, id := range []string{
			"scripted-chat-no-workspace-tools",
			"scripted-implement-short-label",
			"scripted-plan-execute-authorized-new-file",
		} {
			task := taskByID[id]
			gomega.Expect(task.ID).NotTo(gomega.Equal(""), fmt.Sprintf("%s task missing", id))
			gomega.Expect(task.Fixture).To(gomega.Equal("fixtures/frozen-label-repo"), fmt.Sprintf("%s fixture = %q, want frozen-label default", id, task.Fixture))

			assertTaskHasTags(task, "deterministic", "fake-model")
			assertTaskHasRunEvents(task, "idiom.live_contract.rollback")
		}

		temp := taskByID["scripted-implement-delete-file-empty-write-recovery"]
		gomega.Expect(temp.ID).NotTo(gomega.Equal(""), "temp-file task missing")
		gomega.Expect(temp.Fixture).To(gomega.Equal("fixtures/temp-file-repo"), fmt.Sprintf("explicit fixture override = %q, want temp fixture", temp.Fixture))

		assertTaskHasTags(temp, "deterministic", "fake-model")
		assertTaskHasRunEvents(temp, "idiom.live_contract.rollback")
	})
	ginkgo.It("scripted lifecycle and auto-route matrix stay assertion complete", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")
		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		taskByID := make(map[string]eval.SWETask, len(suite.Tasks))
		for _, task := range suite.Tasks {
			taskByID[task.ID] = task
		}

		lifecycle := taskByID["scripted-implement-multi-path-pending-verification-writes"]
		gomega.Expect(lifecycle.ID).NotTo(gomega.BeEmpty(), "multi-path verification lifecycle task missing")
		assertTaskHasTags(lifecycle, "multi-path", "mutation-success", "post-write-verification", "tool-surface-transition", "pending-verification-write-surface")
		assertTaskHasRunEvents(lifecycle, "post_write_verification_required", "idiom.scripted_model.script_fully_consumed")
		gomega.Expect(lifecycle.Scenario.RequireOrderedEvents).To(gomega.HaveLen(8), fmt.Sprintf("lifecycle must pin both allowed writes, the unchanged bounded read, both verification results, both post-verification reads, and the accepted final, got %#v", lifecycle.Scenario.RequireOrderedEvents))
		gomega.Expect(lifecycle.Scenario.RequireModelRequests).To(gomega.Equal([]eval.ModelRequestExpectation{
			{Index: 1, Transport: "textual", RequireTools: []string{"read_file", "apply_patch", "edit_file", "source_sanity"}},
			{Index: 2, Transport: "textual", RequireTools: []string{"read_file", "apply_patch", "edit_file", "source_sanity"}},
		}), fmt.Sprintf("lifecycle must assert writes and verification coexist before B and after B while verification is pending, got %#v", lifecycle.Scenario.RequireModelRequests))
		gomega.Expect(lifecycle.Scenario.RequireTypedFacts).NotTo(gomega.BeNil(), "multi-path lifecycle must use typed exact execution counts")
		executionCounts := make(map[string]int, len(lifecycle.Scenario.RequireTypedFacts.ToolExecutions))
		for _, execution := range lifecycle.Scenario.RequireTypedFacts.ToolExecutions {
			gomega.Expect(execution.ExactCount).NotTo(gomega.BeNil(), fmt.Sprintf("%s exact count missing", execution.Tool))
			executionCounts[execution.Tool] = *execution.ExactCount
		}
		gomega.Expect(executionCounts).To(gomega.Equal(map[string]int{"apply_patch": 2, "read_file": 3, "source_sanity": 2}))
		gomega.Expect(lifecycle.Oracle.ExpectedRecoveries).To(gomega.BeEmpty(), fmt.Sprintf("happy-path lifecycle must not account for a harness rejection, got %#v", lifecycle.Oracle.ExpectedRecoveries))
		gomega.Expect(lifecycle.Oracle.RequireNoRunnerRejections).To(gomega.BeTrue())

		freshRequest := taskByID["scripted-implement-source-prerequisite-fresh-request"]
		gomega.Expect(freshRequest.Scenario.RequireTypedFacts).NotTo(gomega.BeNil(), "fresh-request recovery must retain typed pending paths and exact execution counts")
		gomega.Expect(freshRequest.Scenario.RequireTypedFacts.ToolRejections).To(gomega.HaveLen(1))
		retained := freshRequest.Scenario.RequireTypedFacts.ToolRejections[0]
		gomega.Expect(retained.Tool).To(gomega.Equal("edit_file"))
		gomega.Expect(retained.Disposition).To(gomega.Equal("deferred_by_prerequisite"))
		gomega.Expect(retained.PendingWritePaths).To(gomega.Equal([]string{"internal/labels/labels.go"}))
		gomega.Expect(retained.PendingSourcePaths).To(gomega.Equal([]string{"internal/labels/labels.go"}))
		gomega.Expect(retained.ExactCount).NotTo(gomega.BeNil())
		gomega.Expect(*retained.ExactCount).To(gomega.Equal(1))

		citation := taskByID["scripted-information-directory-ranged-citation"]
		gomega.Expect(citation.ID).NotTo(gomega.BeEmpty(), "directory plus ranged-file citation journey missing")
		assertTaskHasTags(citation, "directory-citation", "ranged-citation", "privacy-safe-source-window", "model-authored-final")
		gomega.Expect(citation.Scenario.RequireTypedFacts).NotTo(gomega.BeNil())

		staleCitation := taskByID["scripted-implement-stale-ranged-citation-refresh"]
		gomega.Expect(staleCitation.ID).NotTo(gomega.BeEmpty(), "stale ranged citation refresh journey missing")
		assertTaskHasTags(staleCitation, "stale-source-window-recovery", "ranged-citation", "privacy-safe-source-window")
		gomega.Expect(staleCitation.Scenario.RequireRejectedTools).To(gomega.ContainElement("completion_gate"))

		for id, initialLane := range map[string]string{
			"scripted-classifier-locate-short-label":                     "locate",
			"scripted-classifier-architecture-fixture-boundary":          "architecture",
			"scripted-classifier-security-review-label":                  "review",
			"scripted-classifier-explore-then-implement-route":           "explore",
			"scripted-classifier-natural-language-implement-short-label": "implement",
			"scripted-classifier-ambiguous-question-read-only":           "main",
		} {
			task := taskByID[id]
			gomega.Expect(task.ID).NotTo(gomega.BeEmpty(), fmt.Sprintf("auto-route task %s missing", id))
			gomega.Expect(task.Scenario.RequireRoutePlan).NotTo(gomega.BeNil(), fmt.Sprintf("auto-route task %s must assert a typed route plan", id))
			gomega.Expect(task.Scenario.RequireRoutePlan.InitialLane).To(gomega.Equal(initialLane), fmt.Sprintf("auto-route task %s initial lane", id))
			assertTaskHasRunEvents(task, "idiom.scripted_model.script_fully_consumed")
		}
		gomega.Expect(taskByID["scripted-classifier-explore-then-implement-route"].Scenario.RequireRoutePlan.FollowupLane).To(gomega.Equal("implement"))
	})
	ginkgo.It("scripted contract generic harness class tasks pinned", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")

		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		taskByID := map[string]eval.SWETask{}
		for _, task := range suite.Tasks {
			taskByID[task.ID] = task
		}

		chat, ok := taskByID["scripted-chat-no-workspace-tools"]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("generic no-tool chat task missing"))

		assertTaskHasTags(chat, "chat", "no-tool", "read-only", "rollback")
		assertTaskHasRunEvents(chat,
			"idiom.live_contract.workspace_prepared",
			"idiom.live_contract.resolved_config",
			"idiom.live_contract.rollback",
			"idiom.scripted_model.requests",
		)
		gomega.Expect(chat.Family).To(gomega.Equal("general_chat"), fmt.Sprintf("generic chat task must assert zero workspace-tool use and no mutation, family=%q oracle=%#v", chat.Family, chat.Oracle))
		gomega.Expect(chat.Oracle.MaxToolRequests).NotTo(gomega.BeNil(), fmt.Sprintf("generic chat task must assert zero workspace-tool use and no mutation, family=%q oracle=%#v", chat.Family, chat.Oracle))
		gomega.Expect(*chat.Oracle.MaxToolRequests).To(gomega.BeZero(), fmt.Sprintf("generic chat task must assert zero workspace-tool use and no mutation, family=%q oracle=%#v", chat.Family, chat.Oracle))
		gomega.Expect(chat.Oracle.RequireNoChangedFiles).To(gomega.BeTrue(), fmt.Sprintf("generic chat task must assert zero workspace-tool use and no mutation, family=%q oracle=%#v", chat.Family, chat.Oracle))

		for _, forbidden := range []string{"read_file", "shell", "edit_file", "submit_plan"} {
			gomega.Expect(slices.Contains(chat.Scenario.ForbidTools, forbidden)).To(gomega.BeTrue(), fmt.Sprintf("generic chat task must forbid %q, got %#v", forbidden, chat.Scenario.ForbidTools))

		}

		deniedRead, ok := taskByID["scripted-workspace-tool-rules-deny-read-file"]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("workspace tool_rules deny read_file task missing"))

		assertTaskHasTags(deniedRead, "information", "tool-rules", "typed-blocker", "read-only", "rollback")
		assertTaskHasRunEvents(deniedRead, "permission_decision", "tool_policy_result", "completion_status")
		gomega.Expect(deniedRead.Family).To(gomega.Equal("information_request"), fmt.Sprintf("workspace tool_rules deny task must be an information request, got %q", deniedRead.Family))
		gomega.Expect(deniedRead.Env["IDIOM_WORKSPACE_TOOL_RULES_JSON"]).To(gomega.ContainSubstring(`"decision":"deny"`), fmt.Sprintf("workspace tool_rules deny task must configure a denied sentinel file, env=%#v", deniedRead.Env))
		gomega.Expect(deniedRead.Env["IDIOM_WORKSPACE_EXTRA_FILES_JSON"]).To(gomega.ContainSubstring("SECRET_SENTINEL_DO_NOT_LEAK"), fmt.Sprintf("workspace tool_rules deny task must configure a denied sentinel file, env=%#v", deniedRead.Env))
		gomega.Expect(deniedRead.Scenario.RequireTools).To(gomega.ContainElement("read_file"), fmt.Sprintf("workspace tool_rules deny task must require one read and forbid completion-gate recovery, scenario=%#v", deniedRead.Scenario))
		gomega.Expect(deniedRead.Scenario.ForbidTools).To(gomega.ContainElement("completion_gate"), fmt.Sprintf("workspace tool_rules deny task must require one read and forbid completion-gate recovery, scenario=%#v", deniedRead.Scenario))
		gomega.Expect(slices.Contains(deniedRead.Scenario.ForbidFinalAssistantContains, "SECRET_SENTINEL_DO_NOT_LEAK")).To(gomega.BeTrue(), fmt.Sprintf("workspace tool_rules deny task must forbid sentinel leakage, scenario=%#v", deniedRead.Scenario))
		gomega.Expect(deniedRead.Oracle.MaxToolRequests).NotTo(gomega.BeNil(), fmt.Sprintf("workspace tool_rules deny task must pin one denied tool request and no mutation, oracle=%#v", deniedRead.Oracle))
		gomega.Expect(*deniedRead.Oracle.MaxToolRequests).To(gomega.Equal(1), fmt.Sprintf("workspace tool_rules deny task must pin one denied tool request and no mutation, oracle=%#v", deniedRead.Oracle))
		gomega.Expect(deniedRead.Oracle.MaxErrors).NotTo(gomega.BeNil(), fmt.Sprintf("workspace tool_rules deny task must pin one denied tool request and no mutation, oracle=%#v", deniedRead.Oracle))
		gomega.Expect(*deniedRead.Oracle.MaxErrors).To(gomega.Equal(1), fmt.Sprintf("workspace tool_rules deny task must pin one denied tool request and no mutation, oracle=%#v", deniedRead.Oracle))
		gomega.Expect(deniedRead.Oracle.RequireNoChangedFiles).To(gomega.BeTrue(), fmt.Sprintf("workspace tool_rules deny task must pin one denied tool request and no mutation, oracle=%#v", deniedRead.Oracle))

		info, ok := taskByID["scripted-information-readme-grounded-answer"]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("grounded information task missing"))

		assertTaskHasTags(info, "information", "grounded-answer", "read-only", "rollback")
		gomega.Expect(info.Family).To(gomega.Equal("information_request"), fmt.Sprintf("information task must require README evidence and groundedness, family=%q scenario=%#v oracle=%#v", info.Family, info.Scenario, info.Oracle))
		gomega.Expect(info.Oracle.Groundedness).NotTo(gomega.BeNil(), fmt.Sprintf("information task must require README evidence and groundedness, family=%q scenario=%#v oracle=%#v", info.Family, info.Scenario, info.Oracle))
		gomega.Expect(info.Scenario.RequireEvidencePaths).To(gomega.ContainElement("README.md"), fmt.Sprintf("information task must require README evidence and groundedness, family=%q scenario=%#v oracle=%#v", info.Family, info.Scenario, info.Oracle))
		gomega.Expect(info.Scenario.RequireTools).To(gomega.ContainElement("read_file"), fmt.Sprintf("information task must allow exactly source-backed read behavior, scenario=%#v", info.Scenario))
		gomega.Expect(info.Scenario.ForbidTools).NotTo(gomega.ContainElement("read_file"), fmt.Sprintf("information task must allow exactly source-backed read behavior, scenario=%#v", info.Scenario))

		help, ok := taskByID["scripted-coding-help-test-advice-read-only"]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("read-only coding-help task missing"))

		assertTaskHasTags(help, "coding-help", "grounded-answer", "read-only", "rollback")
		gomega.Expect(help.Oracle.Groundedness).NotTo(gomega.Equal(nil), fmt.Sprintf("coding-help task must require groundedness, oracle=%#v", help.Oracle))

		for _, want := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go"} {
			gomega.Expect(slices.Contains(help.Scenario.RequireEvidencePaths, want)).To(gomega.BeTrue(), fmt.Sprintf("coding-help task must require grounded evidence path %q, scenario=%#v oracle=%#v", want, help.Scenario, help.Oracle))

		}
		gomega.Expect(help.Oracle.Groundedness.ContextFragments).NotTo(gomega.BeEmpty(), fmt.Sprintf("coding-help task must include fixture-local groundedness fragments, oracle=%#v", help.Oracle))
		gomega.Expect(help.Family).To(gomega.Equal("coding_help"), fmt.Sprintf("coding-help task must stay read-only, family=%q scenario=%#v oracle=%#v", help.Family, help.Scenario, help.Oracle))
		gomega.Expect(help.Scenario.RequireVerification).To(gomega.Equal("none"), fmt.Sprintf("coding-help task must stay read-only, family=%q scenario=%#v oracle=%#v", help.Family, help.Scenario, help.Oracle))
		gomega.Expect(help.Oracle.RequireNoShell).To(gomega.BeTrue(), fmt.Sprintf("coding-help task must stay read-only, family=%q scenario=%#v oracle=%#v", help.Family, help.Scenario, help.Oracle))
		gomega.Expect(help.Oracle.RequireNoChangedFiles).To(gomega.BeTrue(), fmt.Sprintf("coding-help task must stay read-only, family=%q scenario=%#v oracle=%#v", help.Family, help.Scenario, help.Oracle))

	})
	ginkgo.It("scripted contract replay coverage tasks pinned", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")

		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		taskByID := map[string]eval.SWETask{}
		for _, task := range suite.Tasks {
			taskByID[task.ID] = task
		}

		blocked, ok := taskByID["scripted-contract-replay-blocked-post-write-completion"]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("blocked contract replay task missing"))

		assertTaskHasTags(blocked, "contract-replay", "gate-decision", "post-write-verification", "recovery")
		assertTaskHasRunEvents(blocked, "autonomy_event", "post_write_verification_required", "completion_gate_recovery")
		gomega.Expect(blocked.Scenario.RequireVerification).To(gomega.Equal("passed"), fmt.Sprintf("blocked replay task must recover after one gate rejection, scenario=%#v oracle=%#v", blocked.Scenario, blocked.Oracle))
		gomega.Expect(blocked.Oracle.MaxErrors).NotTo(gomega.BeNil(), fmt.Sprintf("blocked replay task must recover after one gate rejection, scenario=%#v oracle=%#v", blocked.Scenario, blocked.Oracle))
		gomega.Expect(*blocked.Oracle.MaxErrors).To(gomega.Equal(1), fmt.Sprintf("blocked replay task must recover after one gate rejection, scenario=%#v oracle=%#v", blocked.Scenario, blocked.Oracle))

		valid, ok := taskByID["scripted-contract-replay-valid-post-write-verification"]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("valid contract replay task missing"))

		assertTaskHasTags(valid, "contract-replay", "gate-decision", "post-write-verification")
		assertTaskHasRunEvents(valid, "autonomy_event", "post_write_verification_required")
		gomega.Expect(slices.Contains(valid.Scenario.RequireRunEvents, "completion_gate_recovery")).To(gomega.BeFalse(), fmt.Sprintf("valid replay task should not require a completion-gate recovery event"))
		gomega.Expect(valid.Scenario.RequireVerification).To(gomega.Equal("passed"), fmt.Sprintf("valid replay task must require clean post-write verification, scenario=%#v oracle=%#v", valid.Scenario, valid.Oracle))
		gomega.Expect(valid.Oracle.RequireNoRunnerRejections).To(gomega.BeTrue(), fmt.Sprintf("valid replay task must require clean post-write verification, scenario=%#v oracle=%#v", valid.Scenario, valid.Oracle))

	})
	ginkgo.It("scripted contract tdd expansion tasks exercise suite behavior", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")

		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		taskByID := map[string]eval.SWETask{}
		for _, task := range suite.Tasks {
			taskByID[task.ID] = task
		}

		for _, tc := range []struct {
			id  string
			tag string
		}{
			{
				id:  "scripted-feature-planner-tui-tdd-state-machine-atom",
				tag: "state-machine",
			},
			{
				id:  "scripted-feature-planner-tui-tdd-boundary-value-atom",
				tag: "boundary-value",
			},
			{
				id:  "scripted-feature-planner-tui-tdd-fault-tree-atom",
				tag: "fault-tree",
			},
			{
				id:  "scripted-feature-planner-tui-tdd-post-mortem-atom",
				tag: "post-mortem",
			},
			{
				id:  "scripted-feature-planner-tui-tdd-formal-verification-atom",
				tag: "formal-verification",
			},
		} {
			task, ok := taskByID[tc.id]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s task missing", tc.id))

			assertTaskHasTags(task, "feature-planner", "tui", "tdd", "sdd", "slash-command", tc.tag)
			assertTaskHasRunEvents(task,
				"idiom.feature_planner.start",
				"idiom.feature_planner.tdd_pre_execution_checked",
				"idiom.feature_planner.plan_accepted",
				"idiom.feature_planner.atom_dispatched",
				"internal_idiom.plan_execute.completed",
				"idiom.feature_planner.atom_source_satisfied",
				"idiom.feature_planner.complete",
				"idiom.live_contract.rollback",
			)
			gomega.Expect(task.Scenario.FinalAssistantContains).To(gomega.Equal([]string{"feature-planner: complete"}), fmt.Sprintf("%s must keep internal atom/TDD/SDD identifiers out of the user-facing completion oracle, got %#v", tc.id, task.Scenario.FinalAssistantContains))
			gomega.Expect(task.Scenario.RequireVerification).To(gomega.Equal("none"), fmt.Sprintf("%s must be a no-edit TUI regression with clean rollback oracle, scenario=%#v oracle=%#v", tc.id, task.Scenario, task.Oracle))
			gomega.Expect(task.Oracle.RequireCleanRollback).To(gomega.BeTrue(), fmt.Sprintf("%s must be a no-edit TUI regression with clean rollback oracle, scenario=%#v oracle=%#v", tc.id, task.Scenario, task.Oracle))
			gomega.Expect(task.Oracle.RequireNoChangedFiles).To(gomega.BeTrue(), fmt.Sprintf("%s must be a no-edit TUI regression with clean rollback oracle, scenario=%#v oracle=%#v", tc.id, task.Scenario, task.Oracle))

		}
	})
	ginkgo.It("scripted contract redis clone testbed task exercises real fixture", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")

		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		var task eval.SWETask

		for _, candidate := range suite.Tasks {
			if candidate.ID == "scripted-feature-planner-tui-redis-clone-mget" {
				task = candidate
				break
			}
		}
		gomega.Expect(task.ID).NotTo(gomega.Equal(""), "redis clone feature-planner testbed task missing")
		gomega.Expect(task.Fixture).To(gomega.Equal("fixtures/redis-clone-repo"), fmt.Sprintf("redis clone task fixture = %q, want fixtures/redis-clone-repo", task.Fixture))

		assertTaskHasTags(task, "feature-planner", "tui", "tdd", "sdd", "slash-command", "plan-execute", "redis-clone", "large-testbed", "mutation-denial", "recovery", "workspace-approval-denial")
		assertTaskHasRunEvents(task,
			"idiom.feature_planner.start",
			"idiom.feature_planner.tdd_pre_execution_checked",
			"idiom.feature_planner.plan_accepted",
			"idiom.feature_planner.atom_dispatched",
			"idiom.feature_planner.plan_execute_git_history_requirement",
			"internal_idiom.plan_execute.blocked",
			"idiom.feature_planner.plan_execute_completion_rejected",
			"idiom.feature_planner.recovery_open",
			"idiom.live_contract.rollback",
		)
		gomega.Expect(task.Scenario.FinalAssistantContains).To(gomega.Equal([]string{"feature-planner: recovery-open"}), fmt.Sprintf("redis clone approval denial must render the typed recovery state without internal atom identifiers, got %#v", task.Scenario.FinalAssistantContains))
		gomega.Expect(task.Scenario.RequireChangedFiles).To(gomega.BeEmpty(), fmt.Sprintf("redis clone approval denial must not claim changed files, scenario=%#v", task.Scenario))
		gomega.Expect(task.Scenario.RequireVerification).To(gomega.Equal("none"), fmt.Sprintf("redis clone task must be a rollback-scored TUI mutation regression, scenario=%#v oracle=%#v", task.Scenario, task.Oracle))
		gomega.Expect(task.Oracle.RequireCleanRollback).To(gomega.BeTrue(), fmt.Sprintf("redis clone task must be a rollback-scored TUI mutation regression, scenario=%#v oracle=%#v", task.Scenario, task.Oracle))
		gomega.Expect(task.Oracle.RequireNoChangedFiles).To(gomega.BeTrue(), fmt.Sprintf("redis clone approval denial must require no changed files, scenario=%#v oracle=%#v", task.Scenario, task.Oracle))
		gomega.Expect(task.Oracle.AllowNoWrite).To(gomega.BeTrue(), fmt.Sprintf("redis clone approval denial must explicitly allow its typed no-write recovery outcome, oracle=%#v", task.Oracle))

	})
	ginkgo.It("scripted contract feature planner direct and product workflow coverage separated", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")

		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		taskByID := map[string]eval.SWETask{}
		for _, task := range suite.Tasks {
			taskByID[task.ID] = task
		}

		if _, ok := taskByID["scripted-feature-planner-tui-product-workflow-e2e"]; ok {
			ginkgo.Fail("plain /feature-planner product-workflow coverage task should be relabeled as direct plan-execute coverage")
		}

		direct, ok := taskByID["scripted-feature-planner-tui-direct-greeting-plan-execute"]
		gomega.Expect(ok).To(gomega.BeTrue(), "direct /feature-planner nested plan-execute task missing")

		assertTaskHasTags(direct, "feature-planner", "tui", "slash-command", "plan-execute")
		assertTaskLacksTags(direct, "product-workflow", "prd", "adr")
		gomega.Expect(direct.Turns).To(gomega.HaveLen(2), fmt.Sprintf("direct task must exercise the planner question and approval turns, got %#v", direct.Turns))
		gomega.Expect(direct.Turns[0]).To(gomega.HavePrefix("/feature-planner "), fmt.Sprintf("direct task first turn should remain plain /feature-planner nested plan-execute coverage, got %q", direct.Turns[0]))
		gomega.Expect(direct.Turns[0]).To(gomega.ContainSubstring("nested plan-execute"), fmt.Sprintf("direct task first turn should retain nested plan-execute coverage, got %q", direct.Turns[0]))

		assertTaskHasRunEvents(direct,
			"idiom.feature_planner.start",
			"idiom.feature_planner.atom_dispatched",
			"internal_idiom.plan_execute.completed",
			"idiom.feature_planner.complete",
		)

		gomega.Expect(direct.Scenario.FinalAssistantContains).To(gomega.Equal([]string{"feature-planner: complete"}), fmt.Sprintf("direct feature-planner completion must remain privacy-safe, got %#v", direct.Scenario.FinalAssistantContains))

		approvalChat, ok := taskByID["scripted-feature-planner-tui-approval-chat-interactivity"]
		gomega.Expect(ok).To(gomega.BeTrue(), "feature-planner approval/chat interactivity task missing")

		assertTaskHasTags(approvalChat, "feature-planner", "tui", "slash-command", "multi-turn", "approval-required", "chat-interactivity")
		assertTaskHasRunEvents(approvalChat,
			"idiom.feature_planner.start",
			"idiom.feature_planner.planner_question_opened",
			"idiom.feature_planner.plan_tab_updated",
		)
		gomega.Expect(approvalChat.Turns).To(gomega.HaveLen(3), fmt.Sprintf("approval/chat interactivity task must exercise feature-planner, chat question, and approval turns; got %#v", approvalChat.Turns))

		for _, want := range []string{"model-generated", "Approved."} {
			gomega.Expect(slices.Contains(approvalChat.Scenario.PromptHistoryContains, want)).To(gomega.BeTrue(), fmt.Sprintf("approval/chat interactivity prompt history assertions must include %q, got %#v", want, approvalChat.Scenario.PromptHistoryContains))

		}
		gomega.Expect(approvalChat.Scenario.RequireAssistantForEachUser).To(gomega.BeTrue(), "approval/chat interactivity task must require an assistant response for each user turn")
		gomega.Expect(approvalChat.Oracle.MinUserTurns).NotTo(gomega.BeNil(), fmt.Sprintf("approval/chat interactivity oracle must require three user and assistant turns, got users=%v assistants=%v", approvalChat.Oracle.MinUserTurns, approvalChat.Oracle.MinAssistantTurns))
		gomega.Expect(*approvalChat.Oracle.MinUserTurns).To(gomega.BeNumerically(">=", 3), fmt.Sprintf("approval/chat interactivity oracle must require three user and assistant turns, got users=%v assistants=%v", approvalChat.Oracle.MinUserTurns, approvalChat.Oracle.MinAssistantTurns))
		gomega.Expect(approvalChat.Oracle.MinAssistantTurns).NotTo(gomega.BeNil(), fmt.Sprintf("approval/chat interactivity oracle must require three user and assistant turns, got users=%v assistants=%v", approvalChat.Oracle.MinUserTurns, approvalChat.Oracle.MinAssistantTurns))
		gomega.Expect(*approvalChat.Oracle.MinAssistantTurns).To(gomega.BeNumerically(">=", 3), fmt.Sprintf("approval/chat interactivity oracle must require three user and assistant turns, got users=%v assistants=%v", approvalChat.Oracle.MinUserTurns, approvalChat.Oracle.MinAssistantTurns))

		prd, ok := taskByID["scripted-product-create-tui-prd-workflow"]
		gomega.Expect(ok).To(gomega.BeTrue(), "explicit /product-create PRD workflow task missing")

		assertTaskHasTags(prd, "product-workflow", "tui", "prd", "slash-command")
		assertTaskLacksTags(prd, "feature-planner", "plan-execute", "adr")
		gomega.Expect(strings.HasPrefix(prd.Prompt, "/product-create ")).To(gomega.BeTrue(), fmt.Sprintf("PRD workflow coverage must use /product-create, got %q", prd.Prompt))

		adr, ok := taskByID["scripted-product-approve-tui-adr-workflow"]
		gomega.Expect(ok).To(gomega.BeTrue(), "explicit /product-approve ADR workflow task missing")

		assertTaskHasTags(adr, "product-workflow", "tui", "prd", "adr", "slash-command")
		assertTaskLacksTags(adr, "feature-planner", "plan-execute")
		gomega.Expect(strings.HasPrefix(adr.Prompt, "/product-approve ")).To(gomega.BeTrue(), fmt.Sprintf("ADR workflow coverage must use /product-approve, got %q", adr.Prompt))

	})
	ginkgo.It("scripted contract implement new file coverage pinned", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")

		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		taskByID := make(map[string]eval.SWETask, len(suite.Tasks))
		for _, candidate := range suite.Tasks {
			taskByID[candidate.ID] = candidate
		}

		task := taskByID["scripted-implement-new-md-file-no-history"]
		gomega.Expect(task.ID).NotTo(gomega.Equal(""), "implement new markdown file task missing")

		assertTaskHasTags(task, "implement", "new-file", "rollback")
		assertTaskHasScenarioTools(task, "write_file", "shell", "read_file")
		assertTaskHasRunEvents(task,
			"idiom.live_contract.workspace_prepared",
			"idiom.live_contract.resolved_config",
			"idiom.live_contract.rollback",
			"idiom.scripted_model.requests",
		)

		for _, forbidden := range []string{"history_range", "history_symbol", "blame_range", "source_sanity"} {
			gomega.Expect(slices.Contains(task.Scenario.ForbidTools, forbidden)).To(gomega.BeTrue(), fmt.Sprintf("implement new-file task must forbid %s, scenario=%#v", forbidden, task.Scenario))

		}
		gomega.Expect(task.Scenario.RequireChangedFiles).To(gomega.ContainElement("created.md"), fmt.Sprintf("implement new-file task must require created.md change, scenario=%#v oracle=%#v", task.Scenario.RequireChangedFiles, task.Oracle.RequireChangedFiles))
		gomega.Expect(task.Oracle.RequireChangedFiles).To(gomega.ContainElement("created.md"), fmt.Sprintf("implement new-file task must require created.md change, scenario=%#v oracle=%#v", task.Scenario.RequireChangedFiles, task.Oracle.RequireChangedFiles))
		gomega.Expect(task.Oracle.RequireFileContent).To(gomega.HaveLen(1), fmt.Sprintf("implement new-file task must pin created.md content oracle, oracle=%#v", task.Oracle.RequireFileContent))
		gomega.Expect(task.Oracle.RequireFileContent[0].Path).To(gomega.Equal("created.md"), fmt.Sprintf("implement new-file task must pin created.md content oracle, oracle=%#v", task.Oracle.RequireFileContent))
		gomega.Expect(task.Oracle.RequireFileContent[0].Contains).To(gomega.ContainElement("temp"), fmt.Sprintf("implement new-file task must pin created.md content oracle, oracle=%#v", task.Oracle.RequireFileContent))
		gomega.Expect(task.Oracle.RequireNoChangedFiles).To(gomega.BeFalse(), fmt.Sprintf("implement new-file task must be rollback-scored mutation coverage, oracle=%#v", task.Oracle))
		gomega.Expect(task.Oracle.RequireCleanRollback).To(gomega.BeTrue(), fmt.Sprintf("implement new-file task must be rollback-scored mutation coverage, oracle=%#v", task.Oracle))
		gomega.Expect(task.Oracle.RequireNoShell).To(gomega.BeFalse(), fmt.Sprintf("implement new-file task must exercise its declared read-only shell verification, oracle=%#v", task.Oracle))
		gomega.Expect(task.Oracle.RequireNoRunnerRejections).To(gomega.BeTrue(), fmt.Sprintf("implement new-file happy path must not rely on a runner rejection, oracle=%#v", task.Oracle))

	})
	ginkgo.It("scripted contract plan execute new file coverage pinned", func() {
		suitePath := filepath.Join(sweEvalTestRepoRoot(), "testdata", "scripted-contracts", "suite.json")

		suite, err := eval.ReadSWESuite(suitePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scripted contract suite: %v", err))

		taskByID := make(map[string]eval.SWETask, len(suite.Tasks))
		for _, candidate := range suite.Tasks {
			taskByID[candidate.ID] = candidate
		}

		task := taskByID["scripted-plan-execute-authorized-new-file"]
		gomega.Expect(task.ID).NotTo(gomega.Equal(""), "plan-execute authorized new-file task missing")

		assertTaskHasTags(task, "plan-execute", "new-file", "rollback", "go")
		assertTaskHasRunEvents(task,
			"idiom.plan_execute.required_edit_paths_resolved",
			"idiom.plan_execute.plan_artifact_loaded",
			"idiom.plan_execute.executor_handoff_packet",
			"idiom.plan_execute.preflight_ok",
			"idiom.plan_execute.executor_complete",
		)
		gomega.Expect(task.Env["IDIOM_EXTRA_ARGS"]).To(gomega.ContainSubstring("-authorized-new-file internal/labels/generated_label.go"), fmt.Sprintf("new-file task must pass typed new-file authorization, env=%#v", task.Env))
		gomega.Expect(task.Env["IDIOM_LIVE_SNAPSHOT_PATHS_JSON"]).To(gomega.Equal("[\"internal/labels/generated_label.go\"]"), fmt.Sprintf("new-file task must snapshot generated file before rollback, env=%#v", task.Env))
		gomega.Expect(task.Scenario.RequireChangedFiles).To(gomega.ContainElements("internal/labels/generated_label.go", "internal/labels/labels.go"), fmt.Sprintf("new-file task must require generated label and baseline label changes, scenario=%#v oracle=%#v", task.Scenario.RequireChangedFiles, task.Oracle.RequireChangedFiles))
		gomega.Expect(task.Oracle.RequireChangedFiles).To(gomega.ContainElements("internal/labels/generated_label.go", "internal/labels/labels.go"), fmt.Sprintf("new-file task must require generated label and baseline label changes, scenario=%#v oracle=%#v", task.Scenario.RequireChangedFiles, task.Oracle.RequireChangedFiles))
		gomega.Expect(task.Oracle.RequireFileContent).To(gomega.HaveLen(1), fmt.Sprintf("new-file task must pin generated file content oracle, oracle=%#v", task.Oracle.RequireFileContent))
		gomega.Expect(task.Oracle.RequireFileContent[0].Path).To(gomega.Equal("internal/labels/generated_label.go"), fmt.Sprintf("new-file task must pin generated file content oracle, oracle=%#v", task.Oracle.RequireFileContent))
		gomega.Expect(task.Oracle.RequireFileContent[0].Contains).To(gomega.ContainElement("func GeneratedLabel() string"), fmt.Sprintf("new-file task must pin generated file content oracle, oracle=%#v", task.Oracle.RequireFileContent))
		gomega.Expect(slices.Contains(task.Oracle.ForbidChangedFiles, "internal/labels/labels.go")).To(gomega.BeFalse(), fmt.Sprintf("new-file task must allow the baseline label fix required for verification, oracle=%#v", task.Oracle))
		gomega.Expect(task.Oracle.RequireNoChangedFiles).To(gomega.BeFalse(), fmt.Sprintf("new-file task must be rollback-scored executor coverage, oracle=%#v", task.Oracle))
		gomega.Expect(task.Oracle.RequireCleanRollback).To(gomega.BeTrue(), fmt.Sprintf("new-file task must be rollback-scored executor coverage, oracle=%#v", task.Oracle))
		gomega.Expect(task.Oracle.RequireExecutorHandoff).To(gomega.BeTrue(), fmt.Sprintf("new-file task must be rollback-scored executor coverage, oracle=%#v", task.Oracle))

		only := taskByID["scripted-plan-execute-authorized-new-file-only"]
		gomega.Expect(only.ID).NotTo(gomega.Equal(""), "plan-execute authorized new-file-only task missing")

		assertTaskHasTags(only, "plan-execute", "new-file", "rollback", "go")
		gomega.Expect(only.Env["IDIOM_EXTRA_ARGS"]).To(gomega.ContainSubstring("-authorized-new-file internal/labels/generated_only.go"), fmt.Sprintf("new-file-only task must pass typed new-file authorization, env=%#v", only.Env))

		assertTaskHasScenarioTools(only, "submit_plan")
		for _, forbidden := range []string{"read_file", "history_range", "history_symbol"} {
			gomega.Expect(slices.Contains(only.Scenario.ForbidTools, forbidden)).To(gomega.BeTrue(), fmt.Sprintf("new-file-only task must forbid %s before submit_plan, scenario=%#v", forbidden, only.Scenario))

		}
		gomega.Expect(only.Oracle.RequireFileContent).To(gomega.HaveLen(1), fmt.Sprintf("new-file-only task must pin generated-only file content oracle, oracle=%#v", only.Oracle.RequireFileContent))
		gomega.Expect(only.Oracle.RequireFileContent[0].Path).To(gomega.Equal("internal/labels/generated_only.go"), fmt.Sprintf("new-file-only task must pin generated-only file content oracle, oracle=%#v", only.Oracle.RequireFileContent))
		gomega.Expect(only.Oracle.RequireFileContent[0].Contains).To(gomega.ContainElement("func GeneratedOnly() string"), fmt.Sprintf("new-file-only task must pin generated-only file content oracle, oracle=%#v", only.Oracle.RequireFileContent))
		gomega.Expect(slices.Contains(only.Oracle.ForbidChangedFiles, "internal/labels/labels.go")).To(gomega.BeTrue(), fmt.Sprintf("new-file-only task must prove existing files stay untouched, oracle=%#v", only.Oracle))

		missingRead := taskByID["scripted-plan-execute-authorized-new-file-after-missing-read"]
		gomega.Expect(missingRead.ID).NotTo(gomega.Equal(""), "plan-execute authorized missing-target read task missing")

		assertTaskHasTags(missingRead, "plan-execute", "new-file", "missing-target-read", "rollback")
		assertTaskHasScenarioTools(missingRead, "read_file", "submit_plan")
		gomega.Expect(missingRead.Scenario.RequireChangedFiles).To(gomega.ContainElement("internal/labels/missing.go"), fmt.Sprintf("missing-target read task must require the explicitly authorized new file, scenario=%#v", missingRead.Scenario))
		gomega.Expect(missingRead.Oracle.ForbidTypedBlocker).To(gomega.BeTrue(), fmt.Sprintf("authorized missing-target read must proceed through fresh submit rather than a typed blocker, oracle=%#v", missingRead.Oracle))
		gomega.Expect(missingRead.Scenario.RequireOrderedEvents).To(gomega.HaveLen(3), fmt.Sprintf("missing-target read task must order read error, fresh submit, and model-authored final, got %#v", missingRead.Scenario.RequireOrderedEvents))

		reference := taskByID["scripted-plan-execute-reference-readonly-closed-scope"]
		gomega.Expect(reference.ID).NotTo(gomega.Equal(""), "plan-execute reference-readonly closed-scope task missing")

		assertTaskHasTags(reference, "plan-execute", "closed-scope", "rollback", "go")
		assertTaskHasScenarioTools(reference, "read_file", "submit_plan")
		gomega.Expect(reference.Oracle.ForbidChangedFiles).To(gomega.ContainElements("README.md", "internal/labels/labels_test.go"), fmt.Sprintf("reference-readonly task must forbid reference/test changes, oracle=%#v", reference.Oracle))
		gomega.Expect(reference.Scenario.RequireChangedFiles).To(gomega.ContainElement("internal/labels/labels.go"), fmt.Sprintf("reference-readonly task must require only label source change, scenario=%#v oracle=%#v", reference.Scenario, reference.Oracle))
		gomega.Expect(reference.Oracle.RequireChangedFiles).To(gomega.ContainElement("internal/labels/labels.go"), fmt.Sprintf("reference-readonly task must require only label source change, scenario=%#v oracle=%#v", reference.Scenario, reference.Oracle))

	})
})

func mustCommandOutput(name string, args ...string) []byte {

	output, err := exec.Command(name, args...).CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("%s %v failed: %v\n%s", name, args, err, output))

	return output
}

func writeSWEMultiTurnSmokeSuite(path string) {

	suite := `{
  "name": "cli-smoke-suite",
  "tasks": [{
    "id": "direct",
    "family": "direct_question",
    "prompt": "Explain routing.",
    "turns": ["first turn", "second turn"],
    "tags": ["read-only"],
    "scenario": {
      "name": "direct",
      "prompt_contains": ["second turn"],
      "require_assistant_for_each_user": true,
      "require_route_plan": {
        "initial_lane": "main",
        "reason": "direct_conversation",
        "contract": "direct_answer"
      },
      "require_verification": "none",
      "require_approval": false
    },
    "oracle": {
      "require_no_changed_files": true,
      "require_no_approval": true,
      "min_user_turns": 2,
      "min_assistant_turns": 2,
      "max_tool_requests": 2
    }
  }]
}
`
	gomega.Expect(os.WriteFile(path, []byte(suite), 0o644)).To(gomega.Succeed())

}

func writeSWETwoTaskSmokeSuite(path string) {

	suite := `{
  "name": "cli-two-task-suite",
  "tasks": [
    {
      "id": "first",
      "family": "direct_question",
      "prompt": "Explain routing.",
      "tags": ["read-only"],
      "scenario": {
        "name": "first",
        "prompt_contains": ["Explain routing"],
        "require_route_plan": {
          "initial_lane": "main",
          "reason": "direct_conversation",
          "contract": "direct_answer"
        },
        "require_verification": "none",
        "require_approval": false
      },
      "oracle": {
        "require_no_changed_files": true,
        "require_no_approval": true,
        "max_errors": 0
      }
    },
    {
      "id": "second",
      "family": "direct_question",
      "prompt": "Explain routing.",
      "tags": ["read-only"],
      "scenario": {
        "name": "second",
        "prompt_contains": ["Explain routing"],
        "require_route_plan": {
          "initial_lane": "main",
          "reason": "direct_conversation",
          "contract": "direct_answer"
        },
        "require_verification": "none",
        "require_approval": false
      },
      "oracle": {
        "require_no_changed_files": true,
        "require_no_approval": true,
        "max_tool_requests": 2
      }
    }
  ]
}
`
	gomega.Expect(os.WriteFile(path, []byte(suite), 0o644)).To(gomega.Succeed())

}

func writeSWESmokeSuite(path string, logPath string) {

	taskLog := ""
	if strings.TrimSpace(logPath) != "" {
		taskLog = `, "log_path": ` + quoteJSON(logPath)
	}

	suite := `{
  "name": "cli-smoke-suite",
  "tasks": [{
    "id": "direct",
    "family": "direct_question",
    "prompt": "Explain routing.",
    "tags": ["read-only"]` + taskLog + `,
    "scenario": {
      "name": "direct",
      "prompt_contains": ["Explain routing"],
      "require_route_plan": {
        "initial_lane": "main",
        "reason": "direct_conversation",
        "contract": "direct_answer"
      },
      "require_verification": "none",
      "require_approval": false
    },
    "oracle": {
      "require_no_changed_files": true,
      "require_no_approval": true,
      "max_tool_requests": 2
    }
  }]
}
`
	gomega.Expect(os.WriteFile(path, []byte(suite), 0o644)).To(gomega.Succeed())

}

func writeSWESmokeLog(path string) {

	const log = `{"type":"message","role":"user","content":"Explain routing."}
{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}
{"type":"message","role":"control_rails","content":"{\"task_family\":\"conversation\",\"permission_profile\":\"conversation_only\"}"}
{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}
`
	gomega.Expect(os.WriteFile(path, []byte(log), 0o644)).To(gomega.Succeed())

}

func smokeSWETask(logPath string) eval.SWETask {
	return eval.SWETask{ID: "direct", LogPath: logPath}
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func assertTaskHasTags(task eval.SWETask, tags ...string) {

	for _, tag := range tags {
		gomega.Expect(slices.Contains(task.Tags, tag)).To(gomega.BeTrue(), fmt.Sprintf("%s missing tag %q in %#v", task.ID, tag, task.Tags))

	}
}

func assertTaskHasScenarioTools(task eval.SWETask, tools ...string) {

	for _, tool := range tools {
		gomega.Expect(slices.Contains(task.Scenario.RequireTools, tool)).To(gomega.BeTrue(), fmt.Sprintf("task %s missing required tool %q: %#v", task.ID, tool, task.Scenario.RequireTools))

	}
}

func assertTaskLacksTags(task eval.SWETask, tags ...string) {

	for _, tag := range tags {
		gomega.Expect(slices.Contains(task.Tags, tag)).To(gomega.BeFalse(), fmt.Sprintf("%s should not have tag %q in %#v", task.ID, tag, task.Tags))

	}
}

func assertTaskHasRunEvents(task eval.SWETask, events ...string) {

	for _, event := range events {
		gomega.Expect(slices.Contains(task.Scenario.RequireRunEvents, event)).To(gomega.BeTrue(), fmt.Sprintf("%s missing required run event %q in %#v", task.ID, event, task.Scenario.RequireRunEvents))

	}
}
