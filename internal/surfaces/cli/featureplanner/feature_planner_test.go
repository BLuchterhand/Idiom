package featureplanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/autonomy"
	featureplannercontract "idiom/internal/contracts/featureplanner"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/featureplannerproduct"
	coreplanexecute "idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/productworkflow"
)

var _ = ginkgo.It("run feature planner main reads prompt file and honors json", func() {
	configPath := writeMainConfig()
	promptPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "feature.md")
	gomega.Expect(os.WriteFile(promptPath, []byte("plan feature\n"), 0o644)).To(gomega.Succeed())

	original := runHeadlessFeaturePlanner
	ginkgo.DeferCleanup(func() { runHeadlessFeaturePlanner = original })

	var got featurePlannerRequest

	runHeadlessFeaturePlanner = func(_ context.Context, req featurePlannerRequest, w io.Writer) (featurePlannerResult, error) {
		got = req
		_, _ = w.Write([]byte("{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"planned\"}\n"))

		return featurePlannerResult{Answer: "planned", Fields: map[string]any{"plan_id": "fp-test"}}, nil
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt-file", promptPath, "-json", "-resume", "prior"},
		&stdout,
		&stderr,
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(got.Prompt).To(gomega.Equal("plan feature"), fmt.Sprintf("unexpected request: %#v", got))
	gomega.Expect(got.Resume).To(gomega.Equal("prior"), fmt.Sprintf("unexpected request: %#v", got))
	gomega.Expect(got.SessionID).To(gomega.Equal("prior"), fmt.Sprintf("unexpected request: %#v", got))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"idiom.feature_planner.start"`), fmt.Sprintf("missing feature-planner JSON events: %s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"idiom.feature_planner.complete"`), fmt.Sprintf("missing feature-planner JSON events: %s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"plan_id":"fp-test"`), fmt.Sprintf("missing feature-planner JSON events: %s", stdout.String()))
	gomega.Expect(strings.Count(stdout.String(), `"session_id":"prior"`)).To(gomega.BeNumerically(">=", 2), fmt.Sprintf("resume run should keep prior session id in JSON stream: %s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"content":"planned"`), fmt.Sprintf("JSON mode should emit the structured final answer, got %q", stdout.String()))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("planned\n"), fmt.Sprintf("JSON mode should not append plain final answer, got %q", stdout.String()))

})
var _ = ginkgo.It("run feature planner main json messages do not leak raw prompt or answer", func() {
	configPath := writeMainConfig()
	secret := "secret-feature-planner-token"
	promptPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "feature.md")
	gomega.Expect(os.WriteFile(promptPath, []byte("plan feature "+secret+"\n"), 0o644)).To(gomega.Succeed())

	originalRunner := runHeadlessFeaturePlanner
	ginkgo.DeferCleanup(func() { runHeadlessFeaturePlanner = originalRunner })
	runHeadlessFeaturePlanner = func(_ context.Context, req featurePlannerRequest, _ io.Writer) (featurePlannerResult, error) {
		gomega.Expect(req.Prompt).To(gomega.ContainSubstring(secret), fmt.Sprintf("runner should still receive raw prompt"))

		return featurePlannerResult{
			Answer: "assistant answer " + secret,
			Fields: map[string]any{"status": "ok"},
		}, nil
	}

	var stdout, stderr bytes.Buffer
	code := RunMainWithTelemetry([]string{"--config", configPath, "--prompt-file", promptPath, "--json"}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("RunMain code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(secret), fmt.Sprintf("json stdout leaked raw prompt or answer:\n%s", stdout.String()))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("assistant answer"), fmt.Sprintf("json stdout leaked raw prompt or answer:\n%s", stdout.String()))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("plan feature"), fmt.Sprintf("json stdout leaked raw prompt or answer:\n%s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"content_hash"`), fmt.Sprintf("json message metadata missing:\n%s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"content_length"`), fmt.Sprintf("json message metadata missing:\n%s", stdout.String()))

})
var _ = ginkgo.It("run feature planner main reports runner error", func() {
	configPath := writeMainConfig()

	original := runHeadlessFeaturePlanner
	ginkgo.DeferCleanup(func() { runHeadlessFeaturePlanner = original })

	runHeadlessFeaturePlanner = func(_ context.Context, _ featurePlannerRequest, _ io.Writer) (featurePlannerResult, error) {
		return featurePlannerResult{}, io.ErrUnexpectedEOF
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "plan feature", "-json"},
		&stdout,
		&stderr,
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected unavailable exit 1, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring(io.ErrUnexpectedEOF.Error()), fmt.Sprintf("stderr should explain runner error, got %s", stderr.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"idiom.feature_planner.error"`), fmt.Sprintf("missing error JSON event: %s", stdout.String()))

})
var _ = ginkgo.It("parse feature planner options rejects prompt conflicts", func() {
	var stderr bytes.Buffer

	_, err := parseFeaturePlannerOptions([]string{"-prompt", "x", "-prompt-file", "brief.md"}, &stderr)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("mutually exclusive")), fmt.Sprintf("expected prompt conflict, got %v", err))

	_, err = parseFeaturePlannerOptions(nil, &stderr)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("provide --prompt")), fmt.Sprintf("expected missing prompt error, got %v", err))

})
var _ = ginkgo.It("default feature planner model turn retries empty planner response", func() {
	var requests []map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()

		gomega.Expect(r.URL.Path).To(gomega.Equal("/v1/chat/completions"), fmt.Sprintf("unexpected path %s", r.URL.Path))

		var payload map[string]any
		gomega.Expect(json.NewDecoder(r.Body).Decode(&payload)).To(gomega.Succeed())

		requests = append(requests, payload)
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"length"}]}`))
			return
		}

		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": featurePlannerPlanExecuteDispatchPlanJSON(),
				},
				"finish_reason": "stop",
			}},
		})

		_, _ = w.Write(body)
	}))

	defer server.Close()

	var stdout bytes.Buffer

	answer, err := defaultFeaturePlannerModelTurn(context.Background(), featurePlannerRequest{
		Config: config.Config{
			LlamaServer: config.LlamaServer{
				Kind:    config.BackendLlama,
				BaseURL: server.URL,
				Model:   "planner-model",
			},
			Chat: config.Chat{RequestTimeoutSeconds: 5},
		},
		JSONEvents: true,
		SessionID:  "retry-empty-planner",
	}, "Plan one feature atom.", string(autonomy.TaskFamilyFeaturePlannerPlanner), &stdout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerModelTurn: %v", err))
	gomega.Expect(answer).To(gomega.ContainSubstring(`"tool": "submit_feature_plan"`), fmt.Sprintf("expected planner JSON answer, got %s", answer))
	gomega.Expect(requests).To(gomega.HaveLen(2), fmt.Sprintf("model requests = %d, want 2", len(requests)))

	if _, ok := requests[0]["response_format"].(map[string]any); !ok {
		ginkgo.Fail(fmt.Sprintf("first request should use JSON response format: %#v", requests[0]))
	}

	if _, ok := requests[1]["response_format"]; ok {
		ginkgo.Fail(fmt.Sprintf("retry request should disable backend JSON response format: %#v", requests[1]))
	}

	if log := stdout.String(); !strings.Contains(log, `"role":"feature_planner_model_empty"`) ||
		!strings.Contains(log, `"role":"feature_planner_model"`) {
		ginkgo.Fail(fmt.Sprintf("expected empty-response diagnostic and final model event, got %s", log))
	}

	for _, forbidden := range []string{
		"Plan one feature atom.",
		`"tool": "submit_feature_plan"`,
	} {
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("feature-planner JSON stream leaked raw planner content %q: %s", forbidden, stdout.String()))

	}
	for _, want := range []string{
		`"content_hash":"sha256:`,
		`"content_length":`,
	} {
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("feature-planner JSON stream missing bounded diagnostic %q: %s", want, stdout.String()))

	}
})
var _ = ginkgo.It("default feature planner model turn reports message emit error", func() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": featurePlannerPlanExecuteDispatchPlanJSON(),
				},
				"finish_reason": "stop",
			}},
		})
		_, _ = w.Write(body)
	}))
	defer server.Close()

	_, err := defaultFeaturePlannerModelTurn(context.Background(), featurePlannerRequest{
		Config: config.Config{
			LlamaServer: config.LlamaServer{
				Kind:    config.BackendLlama,
				BaseURL: server.URL,
				Model:   "planner-model",
			},
			Chat: config.Chat{RequestTimeoutSeconds: 5},
		},
		JSONEvents: true,
		SessionID:  "emit-error",
	}, "Plan one feature atom.", string(autonomy.TaskFamilyFeaturePlannerPlanner), failingWriter{})
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(io.ErrClosedPipe.Error())), fmt.Sprintf("expected emit error, got %v", err))

})

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

var _ = ginkgo.It("feature planner main dispatches through adapter", func() {
	configPath := writeMainConfig()

	original := runHeadlessFeaturePlanner
	ginkgo.DeferCleanup(func() { runHeadlessFeaturePlanner = original })

	called := false

	runHeadlessFeaturePlanner = func(_ context.Context, req featurePlannerRequest, _ io.Writer) (featurePlannerResult, error) {
		called = true
		gomega.Expect(req.Config.Workspace.SessionID).NotTo(gomega.Equal(""), "session id should be set before adapter call")

		return featurePlannerResult{Answer: "ok"}, nil
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"-config", configPath, "-prompt", "plan"}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeZero(), fmt.Sprintf("expected feature-planner dispatch success, code=%d called=%t stdout=%q stderr=%s", code, called, stdout.String(), stderr.String()))
	gomega.Expect(called).To(gomega.BeTrue(), fmt.Sprintf("expected feature-planner dispatch success, code=%d called=%t stdout=%q stderr=%s", code, called, stdout.String(), stderr.String()))
	gomega.Expect(strings.TrimSpace(stdout.String())).To(gomega.Equal("ok"), fmt.Sprintf("expected feature-planner dispatch success, code=%d called=%t stdout=%q stderr=%s", code, called, stdout.String(), stderr.String()))

})
var _ = ginkgo.It("default headless feature planner dispatches atoms through plan execute", func() {
	root := ginkgo.GinkgoT().TempDir()
	configPath := writeMainConfigRoot(root)
	writeFeaturePlannerFixtureFile(filepath.Join(root, "go.mod"), "module idiom.test\n")
	writeFeaturePlannerFixtureFile(filepath.Join(root, "docs", "feature-planner-plan-execute.txt"), "existing feature planner dispatch doc\n")
	writeFeaturePlannerFixtureFile(filepath.Join(root, "cmd", "idiom", "feature_planner_test.go"), "package idiom\n\nimport \"testing\"\n\nfunc TestFixture(t *testing.T) {\n\tif 1 != 1 {\n\t\tt.Fatal(\"unreachable\")\n\t}\n}\n")

	originalModelTurn := runFeaturePlannerModelTurn
	originalPlanExecute := runFeaturePlannerPlanExecute
	ginkgo.DeferCleanup(func() {
		runFeaturePlannerModelTurn = originalModelTurn
		runFeaturePlannerPlanExecute = originalPlanExecute
	})

	modelCalls := 0
	planExecuteCalls := 0

	runFeaturePlannerModelTurn = func(_ context.Context, _ featurePlannerRequest, _ string, taskFamily string, _ io.Writer) (string, error) {
		modelCalls++
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerPlanner)), fmt.Sprintf("feature-planner execution must not use direct model task family %q", taskFamily))

		return featurePlannerPlanExecuteDispatchPlanJSON(), nil
	}
	runFeaturePlannerPlanExecute = func(_ context.Context, _ featurePlannerRequest, prompt string, _ io.Writer) (string, error) {
		planExecuteCalls++
		gomega.Expect(prompt).To(gomega.ContainSubstring("Implement one feature atom"), fmt.Sprintf("plan-execute prompt missing atom contract: %s", prompt))
		gomega.Expect(prompt).To(gomega.ContainSubstring("docs/feature-planner-plan-execute.txt"), fmt.Sprintf("plan-execute prompt missing atom contract: %s", prompt))
		gomega.Expect(prompt).To(gomega.ContainSubstring("Canonical plan-execute edit scope"), fmt.Sprintf("plan-execute prompt missing normalized canonical scope: %s", prompt))
		gomega.Expect(prompt).To(gomega.ContainSubstring("required_edit_paths:"), fmt.Sprintf("plan-execute prompt missing normalized canonical scope: %s", prompt))
		gomega.Expect(prompt).To(gomega.ContainSubstring("cmd/idiom/feature_planner_test.go"), fmt.Sprintf("plan-execute prompt missing normalized canonical scope: %s", prompt))
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring("allowed_edit_paths:"), fmt.Sprintf("promoted required paths must not also be emitted as allowed scope: %s", prompt))

		body, _ := json.Marshal(map[string]any{
			"tool":              featureplannercontract.ToolPlanExecuteComplete,
			"lane_family":       "plan-execute",
			"status":            "completed",
			"completion_source": "test_plan_execute_adapter",
			"summary":           "plan-execute wrote docs/feature-planner-plan-execute.txt and ran go test ./internal/workflows/featureplanner",
		})

		return string(body), nil
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "send atom through plan-execute", "-json"},
		&stdout,
		&stderr,
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("runFeaturePlannerMain exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(modelCalls).To(gomega.Equal(1), fmt.Sprintf("model calls = %d, want planner-only call", modelCalls))
	gomega.Expect(planExecuteCalls).To(gomega.Equal(1), fmt.Sprintf("plan-execute calls = %d, want one atom execution", planExecuteCalls))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("feature-planner-implementor"), fmt.Sprintf("stdout should not expose legacy direct implementor lane: %s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"lane_family":"plan-execute"`), fmt.Sprintf("feature planner event should record plan-execute lane: %s", stdout.String()))

})
var _ = ginkgo.It("default headless feature planner passes context to model turn", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeFeaturePlannerFixtureFile(filepath.Join(root, "go.mod"), "module idiom.test\n")

	originalModelTurn := runFeaturePlannerModelTurn
	ginkgo.DeferCleanup(func() { runFeaturePlannerModelTurn = originalModelTurn })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runFeaturePlannerModelTurn = func(ctx context.Context, _ featurePlannerRequest, _ string, _ string, _ io.Writer) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
			ginkgo.Fail("model turn did not receive canceled run context")
			return "", nil
		}
	}

	_, err := defaultHeadlessFeaturePlanner(ctx, featurePlannerRequest{
		Config: config.Config{
			Workspace: config.Workspace{Root: root, CWD: root},
		},
		Prompt:                "plan canceled feature",
		SessionID:             "feature-planner-canceled",
		MaxPlanningIterations: 1,
	}, io.Discard)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("context canceled")), fmt.Sprintf("expected context cancellation from model turn, got %v", err))

})
var _ = ginkgo.It("default headless feature planner blocks dispatch for in scope surface without tdd obligation", func() {
	root := ginkgo.GinkgoT().TempDir()
	configPath := writeMainConfigRoot(root)
	writeFeaturePlannerFixtureFile(filepath.Join(root, "go.mod"), "module idiom.test\n")
	writeFeaturePlannerFixtureFile(filepath.Join(root, "docs", "feature-planner-plan-execute.txt"), "existing feature planner dispatch doc\n")
	writeFeaturePlannerFixtureFile(filepath.Join(root, "cmd", "idiom", "feature_planner_test.go"), "package idiom\n\nimport \"testing\"\n\nfunc TestFixture(t *testing.T) {\n\tif 1 != 1 {\n\t\tt.Fatal(\"unreachable\")\n\t}\n}\n")

	originalModelTurn := runFeaturePlannerModelTurn
	originalPlanExecute := runFeaturePlannerPlanExecute
	ginkgo.DeferCleanup(func() {
		runFeaturePlannerModelTurn = originalModelTurn
		runFeaturePlannerPlanExecute = originalPlanExecute
	})

	modelCalls := 0
	planExecuteCalls := 0

	runFeaturePlannerModelTurn = func(_ context.Context, _ featurePlannerRequest, _ string, _ string, _ io.Writer) (string, error) {
		modelCalls++
		return featurePlannerPlanExecuteDispatchPlanJSONWithEvidenceOnlySurface(), nil
	}
	runFeaturePlannerPlanExecute = func(_ context.Context, _ featurePlannerRequest, _ string, _ io.Writer) (string, error) {
		planExecuteCalls++
		return "", nil
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "send atom through plan-execute", "-json"},
		&stdout,
		&stderr,
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("runFeaturePlannerMain exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(modelCalls).To(gomega.Equal(3), fmt.Sprintf("model calls = %d, want bounded planner repair attempts", modelCalls))
	gomega.Expect(planExecuteCalls).To(gomega.Equal(0), fmt.Sprintf("plan-execute calls = %d, want dispatch blocked until in-scope surfaces have TDD obligations", planExecuteCalls))

	for _, want := range []string{"in-scope surface missing tdd_obligations", "planning-stuck"} {
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("stdout missing repair evidence %q:\nstdout=%s\nstderr=%s", want, stdout.String(), stderr.String()))

	}
})
var _ = ginkgo.It("feature planner plan execute progress path uses latest state event", func() {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","phase":"executor","status":"running","state_path":".idiom/plan-execute/first/state.json"}`,
		`not json`,
		`{"type":"idiom.plan_execute.resume_state_saved","phase":"complete","status":"completed","review_status":"skipped","review_repair_skipped":true,"state_path":".idiom/plan-execute/final/state.json"}`,
		`{"type":"message","state_path":"ignored"}`,
	}, "\n")

	fields := featureplanner.PlanExecuteCompletionSummaryFromOutput(output).Fields()
	if got := featureplanner.CompletionFieldString(fields, "progress_path"); got != ".idiom/plan-execute/final/state.json" {
		ginkgo.Fail(fmt.Sprintf("progress path = %q", got))
	}

	if got := fmt.Sprint(fields["status"]); got != "completed" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want completed; fields=%+v", got, fields))
	}

	if got := fmt.Sprint(fields["phase"]); got != "complete" {
		ginkgo.Fail(fmt.Sprintf("phase = %q, want complete; fields=%+v", got, fields))
	}

	if got := fmt.Sprint(fields["review_status"]); got != "skipped" {
		ginkgo.Fail(fmt.Sprintf("review_status = %q, want skipped; fields=%+v", got, fields))
	}

	if got := fields["review_repair_skipped"]; got != true {
		ginkgo.Fail(fmt.Sprintf("review_repair_skipped = %v, want true; fields=%+v", got, fields))
	}
})
var _ = ginkgo.It("feature planner plan execute completion fields marks review degraded without state path", func() {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.review_repair_degraded","status":"degraded","reason_class":"review_model_error","status_detail":"review unavailable"}`,
		`{"type":"message","content":"review unavailable"}`,
	}, "\n")

	fields := featureplanner.PlanExecuteCompletionSummaryFromOutput(output).Fields()

	if got := fmt.Sprint(fields["status"]); got != "degraded" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want degraded; fields=%+v", got, fields))
	}

	if got := fmt.Sprint(fields["review_status"]); got != "degraded" {
		ginkgo.Fail(fmt.Sprintf("review_status = %q, want degraded; fields=%+v", got, fields))
	}

	if got := fmt.Sprint(fields["failure_kind"]); got != "review_model_error" {
		ginkgo.Fail(fmt.Sprintf("failure_kind = %q, want review_model_error; fields=%+v", got, fields))
	}
})
var _ = ginkgo.It("default feature planner plan execute returns typed reviewed completion", func() {
	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, _ io.Writer) int {
				gomega.Expect(opts.SkipReviewRepair).To(gomega.BeFalse(), fmt.Sprintf("feature-planner atom dispatch must not bypass plan-execute review-repair, opts=%+v", opts))

				_, _ = stdout.Write([]byte(strings.Join([]string{
					`{"type":"idiom.plan_execute.resume_state_saved","phase":"complete","status":"completed","review_status":"passed","state_path":".idiom/plan-execute/session/state.json"}`,
					`{"type":"message","content":"done"}`,
				}, "\n")))

				return 0
			}),
		},
	}

	prompt := strings.Join([]string{
		"Implement one feature atom.",
		"Active plan contract id: feature/atom-1/scope",
		"Active plan contract snapshot hash: sha256:abc123",
		"This atom brief is consumed by plan-execute. Plan-execute owns the terminal response protocol.",
	}, "\n")
	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, prompt, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["status"]); got != "completed" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want completed; envelope=%+v", got, envelope))
	}

	if got := fmt.Sprint(envelope["review_status"]); got != "passed" {
		ginkgo.Fail(fmt.Sprintf("review_status = %q, want passed; envelope=%+v", got, envelope))
	}

	if _, ok := envelope["review_repair_skipped"]; ok {
		ginkgo.Fail(fmt.Sprintf("headless feature-planner must not synthesize skipped review evidence; envelope=%+v", envelope))
	}

	if got := fmt.Sprint(envelope["completion_source"]); got != "headless_plan_execute_returned" {
		ginkgo.Fail(fmt.Sprintf("completion_source = %q, want headless_plan_execute_returned; envelope=%+v", got, envelope))
	}
	gomega.Expect(envelope["contract_id"]).To(gomega.Equal("feature/atom-1/scope"))
	gomega.Expect(envelope["snapshot_hash"]).To(gomega.Equal("sha256:abc123"))
})
var _ = ginkgo.It("default feature planner plan execute passes product write envelope", func() {
	var gotOptions coreplanexecute.Options

	req := featurePlannerRequest{
		ConfigPath:                   "config.json",
		SessionID:                    "feature-session",
		Config:                       config.Config{LlamaServer: config.LlamaServer{Model: "deepseek-v4-flash"}},
		ProductWriteEnvelope:         []string{"native/src/core/colony_sim.h", "native/tests/generated/**"},
		ProductWriteEnvelopeExplicit: true,
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, _ io.Writer) int {
				gotOptions = opts
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete"}` + "\n"))

				return 0
			}),
		},
	}

	if _, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard); err != nil {
		ginkgo.Fail(fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	}

	for _, want := range []string{"native/src/core/colony_sim.h", "native/tests/generated/**"} {
		gomega.Expect(testContainsString([]string(gotOptions.WriteAllowedPaths), want)).To(gomega.BeTrue(), fmt.Sprintf("missing write envelope path %q in options %#v", want, gotOptions.WriteAllowedPaths))

	}
	gomega.Expect(testContainsString([]string(gotOptions.AllowedEditPaths), "native/src/core/colony_sim.h")).To(gomega.BeFalse(), fmt.Sprintf("write envelope must not be passed as candidate edit scope: %#v", gotOptions.AllowedEditPaths))
	gomega.Expect(gotOptions.WriteAllowedPathsExplicit).To(gomega.BeTrue(), fmt.Sprintf("product write envelope must be marked explicit: %#v", gotOptions))

})
var _ = ginkgo.It("default feature planner plan execute forwards resolved model override", func() {
	var gotOptions coreplanexecute.Options

	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Config:     config.Config{LlamaServer: config.LlamaServer{Model: "deepseek-v4-flash"}},
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, _ io.Writer) int {
				gotOptions = opts
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete"}` + "\n"))

				return 0
			}),
		},
	}

	if _, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard); err != nil {
		ginkgo.Fail(fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	}
	gomega.Expect(gotOptions.Model).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("resolved feature-planner model must be forwarded to nested plan-execute: %#v", gotOptions))

})
var _ = ginkgo.It("default feature planner plan execute passes typed authorized new files", func() {
	var gotOptions coreplanexecute.Options

	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, _ io.Writer) int {
				gotOptions = opts
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete"}` + "\n"))

				return 0
			}),
		},
	}
	prompt := strings.Join([]string{
		"Implement one feature atom.",
		"Typed plan execution contract:",
		`{"revision":"plan_execution_contract.v1","authorized_new_files":[{"path":"pkg/new_helper.go","source":"test"}],"atoms":[{"atom_id":"atom-1","scope":{"required_coverage_paths":["pkg/new_helper.go"]}}]}`,
		"Typed plan authority projection:",
		`{"atom_id":"atom-1"}`,
	}, "\n")

	if _, err := defaultFeaturePlannerPlanExecute(context.Background(), req, prompt, io.Discard); err != nil {
		ginkgo.Fail(fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	}
	gomega.Expect(testContainsString(gotOptions.AuthorizedNewFiles, "pkg/new_helper.go")).To(gomega.BeTrue(), fmt.Sprintf("typed authorized new file missing from options %#v", gotOptions.AuthorizedNewFiles))

})
var _ = ginkgo.It("default feature planner plan execute passes explicit empty product write envelope", func() {
	var gotOptions coreplanexecute.Options

	req := featurePlannerRequest{
		ConfigPath:                   "config.json",
		SessionID:                    "feature-session",
		ProductWriteEnvelopeExplicit: true,
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, _ io.Writer) int {
				gotOptions = opts
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.source_satisfied","status":"source-satisfied","outcome":"source_satisfied","phase":"complete"}` + "\n"))

				return 0
			}),
		},
	}

	if _, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard); err != nil {
		ginkgo.Fail(fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	}
	gomega.Expect(gotOptions.WriteAllowedPathsExplicit).To(gomega.BeTrue(), fmt.Sprintf("empty product write envelope must still be marked explicit: %#v", gotOptions))
	gomega.Expect(gotOptions.WriteAllowedPaths).To(gomega.BeEmpty(), fmt.Sprintf("empty product write envelope should not synthesize path options: %#v", gotOptions.WriteAllowedPaths))

})
var _ = ginkgo.It("default feature planner plan execute marks missing typed status ambiguous", func() {
	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(_ coreplanexecute.Options, stdout io.Writer, _ io.Writer) int {
				_, _ = stdout.Write([]byte("plain nested stdout without typed completion status"))
				return 0
			}),
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["status"]); got != "ambiguous" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want ambiguous; envelope=%+v", got, envelope))
	}

	if got := fmt.Sprint(envelope["failure_kind"]); got != "missing_nested_plan_execute_status" {
		ginkgo.Fail(fmt.Sprintf("failure_kind = %q, want missing_nested_plan_execute_status; envelope=%+v", got, envelope))
	}
})
var _ = ginkgo.It("default feature planner plan execute returns typed failure completion", func() {
	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(_ coreplanexecute.Options, stdout io.Writer, stderr io.Writer) int {
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.resume_state_saved","phase":"review_repair","status":"degraded","review_status":"degraded","state_path":".idiom/plan-execute/session/state.json"}`))
				_, _ = stderr.Write([]byte("review repair degraded"))

				return 1
			}),
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("typed nested failure should be returned to feature planner, got error: %v", err))

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["status"]); got != "degraded" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want degraded; envelope=%+v", got, envelope))
	}

	if got := fmt.Sprint(envelope["failure_reason"]); !strings.Contains(got, "exit 1") {
		ginkgo.Fail(fmt.Sprintf("failure_reason = %q, want exit status; envelope=%+v", got, envelope))
	}

	if got := envelope["exit_code"]; got != float64(1) {
		ginkgo.Fail(fmt.Sprintf("exit_code = %#v, want 1; envelope=%+v", got, envelope))
	}
})
var _ = ginkgo.It("default feature planner plan execute retries anchor refresh blocker", func() {
	var calls int
	var retryPrompt string

	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, stderr io.Writer) int {
				calls++
				if calls == 2 {
					retryPrompt = opts.Prompt
					_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","review_status":"passed","state_path":".idiom/plan-execute/session/state.json"}`))
					return 0
				}

				_, _ = stdout.Write([]byte(strings.Join([]string{
					`{"type":"idiom.plan_execute.resume_state_saved","phase":"planning","status":"failed","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n\nRemaining bounded refresh paths:\n- native/src/core/colony_sim_tick.cpp\nNext valid action: re-run with enough budget and issue exactly one bounded read_file or code_navigate source-body request for a remaining refresh path.","required_edit_paths":["native/src/core/colony_sim_tick.cpp","native/src/simulation/world/collapse_event.h"],"state_path":".idiom/plan-execute/session/state.json"}`,
					`{"type":"idiom.plan_execute.recovery_failure_record_written","failure_kind":"planner_blocked","next_valid_action":"retry_plan_execute"}`,
				}, "\n")))
				_, _ = stderr.Write([]byte("planner blocked"))
				return 1
			}),
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	gomega.Expect(calls).To(gomega.Equal(2), fmt.Sprintf("RunPlanExecute calls = %d, want 2", calls))

	for _, want := range []string{
		"## Plan-execute Recovery Addendum",
		"First read source for `native/src/core/colony_sim_tick.cpp`",
		"then satisfy runtime-required git history evidence",
		"then produce one valid `submit_plan`",
	} {
		gomega.Expect(retryPrompt).To(gomega.ContainSubstring(want), fmt.Sprintf("retry prompt missing %q:\n%s", want, retryPrompt))

	}

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["status"]); got != "completed" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want completed; envelope=%+v", got, envelope))
	}
	gomega.Expect(raw).NotTo(gomega.ContainSubstring("recovery-open"), fmt.Sprintf("successful retry must not return recovery-open completion: %s", raw))

})
var _ = ginkgo.It("default feature planner plan execute retries truncated anchor refresh blocker", func() {
	var calls int
	var retryPrompt string

	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, stderr io.Writer) int {
				calls++
				if calls == 2 {
					retryPrompt = opts.Prompt
					_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","review_status":"passed","state_path":".idiom/plan-execute/session/state.json"}`))
					return 0
				}

				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.resume_state_saved","phase":"planning","status":"failed","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.","required_edit_paths":["native/src/core/colony_sim_tick.cpp","native/src/simulation/world/collapse_event.h"],"state_path":".idiom/plan-execute/session/state.json","next_valid_action":"answer_blocker"}`))
				_, _ = stderr.Write([]byte("planner blocked"))
				return 1
			}),
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	gomega.Expect(calls).To(gomega.Equal(2), fmt.Sprintf("RunPlanExecute calls = %d, want 2", calls))
	gomega.Expect(retryPrompt).To(gomega.ContainSubstring("candidate anchor paths (native/src/core/colony_sim_tick.cpp, native/src/simulation/world/collapse_event.h)"), fmt.Sprintf("retry prompt missing source refresh instruction:\n%s", retryPrompt))
	gomega.Expect(retryPrompt).To(gomega.ContainSubstring("First refresh source-body evidence for each candidate anchor path"), fmt.Sprintf("retry prompt missing source refresh instruction:\n%s", retryPrompt))
	gomega.Expect(retryPrompt).To(gomega.ContainSubstring("then produce one valid `submit_plan`"), fmt.Sprintf("retry prompt missing source refresh instruction:\n%s", retryPrompt))

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["status"]); got != "completed" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want completed; envelope=%+v", got, envelope))
	}
})
var _ = ginkgo.It("default feature planner plan execute does not retry human planner blocker", func() {
	var calls int

	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(_ coreplanexecute.Options, stdout io.Writer, stderr io.Writer) int {
				calls++
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.resume_state_saved","phase":"planning","status":"failed","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: product owner must approve broad new behavior","required_edit_paths":["docs/design.md"],"state_path":".idiom/plan-execute/session/state.json"}`))
				_, _ = stderr.Write([]byte("planner blocked"))
				return 1
			}),
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	gomega.Expect(calls).To(gomega.Equal(1), fmt.Sprintf("RunPlanExecute calls = %d, want 1", calls))

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["status"]); got != "failed" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want failed; envelope=%+v", got, envelope))
	}

	if got := fmt.Sprint(envelope["failure_reason"]); !strings.Contains(got, "product owner must approve") {
		ginkgo.Fail(fmt.Sprintf("failure_reason = %q, want human blocker preserved; envelope=%+v", got, envelope))
	}
})
var _ = ginkgo.It("default feature planner plan execute retries typed retry action without anchor prose", func() {
	var calls int
	var retryPrompt string

	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, stderr io.Writer) int {
				calls++
				if calls == 2 {
					retryPrompt = opts.Prompt
					_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","review_status":"passed","state_path":".idiom/plan-execute/session/state.json"}`))
					return 0
				}

				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.resume_state_saved","phase":"planning","status":"failed","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: source refresh needed","next_valid_action":"retry_plan_execute","required_edit_paths":["native/src/core/colony_sim_tick.cpp","native/src/simulation/world/collapse_event.h"],"state_path":".idiom/plan-execute/session/state.json"}`))
				_, _ = stderr.Write([]byte("planner blocked"))
				return 1
			}),
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	gomega.Expect(calls).To(gomega.Equal(2), fmt.Sprintf("RunPlanExecute calls = %d, want 2", calls))
	gomega.Expect(retryPrompt).To(gomega.ContainSubstring("First refresh source-body evidence for each candidate anchor path"), fmt.Sprintf("typed retry action should refresh candidate required paths:\n%s", retryPrompt))
	gomega.Expect(retryPrompt).To(gomega.ContainSubstring("native/src/core/colony_sim_tick.cpp, native/src/simulation/world/collapse_event.h"), fmt.Sprintf("typed retry action should refresh candidate required paths:\n%s", retryPrompt))

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["status"]); got != "completed" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want completed; envelope=%+v", got, envelope))
	}
})
var _ = ginkgo.It("default feature planner plan execute retries from durable terminal completion", func() {
	root := ginkgo.GinkgoT().TempDir()
	var calls int
	var retryPrompt string
	sessionID := "atom-1-run-1"
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	req := featurePlannerRequest{
		ConfigPath: writeMainConfigRoot(root),
		Config:     cfg,
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(opts coreplanexecute.Options, stdout io.Writer, stderr io.Writer) int {
				calls++
				if calls == 2 {
					retryPrompt = opts.Prompt
					_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","review_status":"passed","state_path":".idiom/plan-execute/session/state.json"}`))
					return 0
				}

				_, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, plancontract.PlanExecuteTerminalCompletionRecord{
					SessionID:         sessionID,
					Status:            plancontract.PlanExecuteTerminalStatusFailed,
					Phase:             plancontract.PlanExecuteProgressPhasePlanning,
					FailureKind:       plancontract.PlanExecuteFailurePlannerBlocked,
					FailureReason:     "<<QUESTION>>: source refresh needed",
					NextAction:        "retry_plan_execute",
					RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"},
				})
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save terminal completion: %v", err))

				_, _ = stderr.Write([]byte("planner blocked"))
				return 1
			}),
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "Implement atom.\nPlan-execute session id: "+sessionID, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	gomega.Expect(calls).To(gomega.Equal(2), fmt.Sprintf("RunPlanExecute calls = %d, want durable terminal retry", calls))
	gomega.Expect(retryPrompt).To(gomega.ContainSubstring("native/src/core/colony_sim_tick.cpp"), fmt.Sprintf("retry prompt should be built from durable terminal sidecar:\n%s", retryPrompt))
	gomega.Expect(retryPrompt).To(gomega.ContainSubstring("then produce one valid `submit_plan`"), fmt.Sprintf("retry prompt should be built from durable terminal sidecar:\n%s", retryPrompt))

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["status"]); got != "completed" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want completed; envelope=%+v", got, envelope))
	}
})
var _ = ginkgo.It("default feature planner plan execute ignores stale durable terminal completion", func() {
	root := ginkgo.GinkgoT().TempDir()
	sessionID := "atom-1-run-1"
	stalePath, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, plancontract.PlanExecuteTerminalCompletionRecord{
		SessionID:         sessionID,
		Status:            plancontract.PlanExecuteTerminalStatusFailed,
		Phase:             plancontract.PlanExecuteProgressPhasePlanning,
		FailureKind:       plancontract.PlanExecuteFailurePlannerBlocked,
		FailureReason:     "<<QUESTION>>: stale source refresh needed",
		NextAction:        "retry_plan_execute",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save stale terminal completion: %v", err))

	staleTime := time.Now().Add(-1 * time.Hour)
	if !filepath.IsAbs(stalePath) {
		stalePath = filepath.Join(root, stalePath)
	}
	gomega.Expect(os.Chtimes(stalePath, staleTime, staleTime)).To(gomega.Succeed())

	var calls int
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	req := featurePlannerRequest{
		ConfigPath: writeMainConfigRoot(root),
		Config:     cfg,
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(_ coreplanexecute.Options, _ io.Writer, stderr io.Writer) int {
				calls++
				_, _ = stderr.Write([]byte("planner blocked without terminal sidecar"))
				return 1
			}),
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "Implement atom.\nPlan-execute session id: "+sessionID, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("defaultFeaturePlannerPlanExecute returned error: %v", err))
	gomega.Expect(calls).To(gomega.Equal(1), fmt.Sprintf("RunPlanExecute calls = %d, want no retry from stale terminal sidecar", calls))

	var envelope map[string]any
	gomega.Expect(json.Unmarshal([]byte(raw), &envelope)).To(gomega.Succeed())

	if got := fmt.Sprint(envelope["next_valid_action"]); got == "retry_plan_execute" {
		ginkgo.Fail(fmt.Sprintf("stale durable terminal sidecar should not contribute retry action: %+v", envelope))
	}
	if got := fmt.Sprint(envelope["status"]); got != "failed" {
		ginkgo.Fail(fmt.Sprintf("status = %q, want failed from current child result; envelope=%+v", got, envelope))
	}
})
var _ = ginkgo.It("default feature planner plan execute emits blocked telemetry for failure", func() {
	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		JSONEvents: true,
		Deps: Dependencies{
			RunPlanExecute: featurePlannerPlanExecuteRunner(func(_ coreplanexecute.Options, stdout io.Writer, stderr io.Writer) int {
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.resume_state_saved","phase":"review_repair","status":"failed","review_status":"review_model_error","state_path":".idiom/plan-execute/session/state.json"}`))
				_, _ = stderr.Write([]byte("review model error"))

				return 1
			}),
		},
	}

	var stdout bytes.Buffer

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", &stdout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("typed nested failure should be returned to feature planner, got error: %v", err))
	gomega.Expect(raw).To(gomega.ContainSubstring(`"status":"failed"`), fmt.Sprintf("completion should preserve failed status: %s", raw))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"`+string(featureplannercontract.NestedPlanExecuteCompletionEventBlocked)+`"`), fmt.Sprintf("expected blocked telemetry, stdout=%s", stdout.String()))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(`"type":"`+string(featureplannercontract.NestedPlanExecuteCompletionEventCompleted)+`"`), fmt.Sprintf("failure completion must not emit completed telemetry, stdout=%s", stdout.String()))

})
var _ = ginkgo.It("default headless feature planner requires explicit product context", func() {
	root := ginkgo.GinkgoT().TempDir()
	originalModelTurn := runFeaturePlannerModelTurn
	ginkgo.DeferCleanup(func() { runFeaturePlannerModelTurn = originalModelTurn })

	runFeaturePlannerModelTurn = func(_ context.Context, _ featurePlannerRequest, _ string, _ string, _ io.Writer) (string, error) {
		ginkgo.Fail("model planner must not run when explicit product effort context cannot load")

		return "", nil
	}

	_, err := defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "implement product feature",
		MaxPlanningIterations: 3,
		EffortRoot:            filepath.Join(root, "missing-effort"),
	}, io.Discard)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("product workflow context unavailable")), fmt.Sprintf("expected product context error, got %v", err))

})
var _ = ginkgo.It("default headless feature planner closed source satisfied skips dispatch", ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"), func() {
	root := ginkgo.GinkgoT().TempDir()
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "closed-effort")
	writeFeaturePlannerResumeEffort(effortRoot, "closed-effort")

	brief, err := featureplannerproduct.LoadBriefFromEffort(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load workflow state: %v", err))
	producerState := featureplanner.NewState("closed-delivery-producer", "inspect existing implementation", "", 3, time.Unix(1, 0).UTC())
	producerState.EffortRoot = effortRoot
	producerState.EffortID = "closed-effort"
	producerState.TaskID = brief.TaskID
	producerState.Status = featureplanner.FeatureStateComplete
	producerState.PlanningLoop.State = featureplanner.FeatureStateComplete
	producerState.Plan.Artifact = featureplanner.FeaturePlanArtifact{
		ArtifactID:           "closed-delivery-plan",
		Revision:             1,
		Digest:               "sha256:" + strings.Repeat("a", 64),
		WorkspaceFingerprint: "workspace-closed-delivery",
	}
	producerState.Plan.Approval = featureplanner.FeaturePlanApproval{
		Status:               "approved",
		EventID:              "evt-closed-delivery-approved",
		PlanRevision:         producerState.Plan.Artifact.Revision,
		PlanDigest:           producerState.Plan.Artifact.Digest,
		WorkspaceFingerprint: producerState.Plan.Artifact.WorkspaceFingerprint,
	}
	atomPlan := featureplanner.FeatureAtomPlan{
		ID:                "atom-existing-implementation",
		RequiredEditPaths: []string{"internal/cmd/featureplanner/feature_planner.go"},
	}
	producerState.Plan.Atoms = []featureplanner.FeatureAtomPlan{atomPlan}
	producerState.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: atomPlan,
		State:           featureplanner.AtomStateSourceSatisfied,
	}}
	producerState.VerificationClosures = []featureplanner.VerificationClosure{{
		EventID:                      "evt-existing-implementation",
		AtomID:                       "atom-existing-implementation",
		Status:                       featureplanner.VerificationClosureStatusSourceSatisfied,
		Outcome:                      string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied),
		PlanArtifactID:               producerState.Plan.Artifact.ArtifactID,
		PlanRevision:                 producerState.Plan.Artifact.Revision,
		PlanDigest:                   producerState.Plan.Artifact.Digest,
		ApprovalEventID:              producerState.Plan.Approval.EventID,
		WorkspaceFingerprint:         producerState.Plan.Artifact.WorkspaceFingerprint,
		SourceSatisfiedEvidencePaths: []string{"internal/cmd/featureplanner/feature_planner.go"},
		SourceSatisfiedEvidence: []plancontract.SourceSatisfiedEvidenceRef{{
			Path: "internal/cmd/featureplanner/feature_planner.go",
			Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
		}},
		CreatedAt: "2026-07-12T12:00:00Z",
	}}
	producerState.Continuation.CompletedAtoms = []string{"atom-existing-implementation"}
	producerState.ReleaseReadiness = featureplanner.EvaluateReleaseReadiness(producerState, "2026-07-12T12:00:01Z")
	gomega.Expect(producerState.ReleaseReadiness.Status).To(gomega.Equal(featureplanner.ReleaseReadinessStatusReady))
	gomega.Expect(featureplanner.ValidateDurableState(producerState)).To(gomega.Succeed())
	recorded, err := featureplannerproduct.FinalizeSourceSatisfiedDeliveryFromFeatureState(brief, producerState)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("record source-satisfied delivery: %v", err))
	gomega.Expect(recorded).To(gomega.BeTrue())
	workflowState, err := productworkflow.LoadWorkflowState(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load recorded workflow state: %v", err))
	gomega.Expect(workflowState.CurrentPhase).To(gomega.Equal(string(productworkflow.PhaseComplete)))
	gomega.Expect(workflowState.SourceSatisfiedDelivery).NotTo(gomega.BeNil())

	originalModelTurn := runFeaturePlannerModelTurn
	originalPlanExecute := runFeaturePlannerPlanExecute
	ginkgo.DeferCleanup(func() {
		runFeaturePlannerModelTurn = originalModelTurn
		runFeaturePlannerPlanExecute = originalPlanExecute
	})

	runFeaturePlannerModelTurn = func(_ context.Context, _ featurePlannerRequest, _ string, _ string, _ io.Writer) (string, error) {
		ginkgo.Fail("closed/source-satisfied product effort must not call planner model")

		return "", nil
	}
	runFeaturePlannerPlanExecute = func(_ context.Context, _ featurePlannerRequest, _ string, _ io.Writer) (string, error) {
		ginkgo.Fail("closed/source-satisfied product effort must not dispatch plan-execute")

		return "", nil
	}

	var stdout bytes.Buffer

	result, err := defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "dispatch original closed atom",
		SessionID:             "closed-source-satisfied",
		EffortRoot:            effortRoot,
		EffortID:              "closed-effort",
		MaxPlanningIterations: 3,
		JSONEvents:            true,
	}, &stdout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("closed source-satisfied run: %v", err))
	gomega.Expect(result.Fields["status"]).To(gomega.Equal(featureplanner.FeatureStateSourceSatisfied), fmt.Sprintf("expected source-satisfied result fields, got %#v", result.Fields))
	gomega.Expect(result.Answer).To(gomega.ContainSubstring("source-satisfied"), fmt.Sprintf("answer should report source-satisfied state, got %q", result.Answer))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring("product_source_satisfied"), fmt.Sprintf("expected product_source_satisfied event, stdout=%s", stdout.String()))

	state, _, err := featureplanner.LoadState(root, "closed-source-satisfied")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load saved state: %v", err))
	gomega.Expect(state.Status).To(gomega.Equal(featureplanner.FeatureStateSourceSatisfied), fmt.Sprintf("unexpected saved state: %#v", state))
	gomega.Expect(state.EffortID).To(gomega.Equal("closed-effort"), fmt.Sprintf("unexpected saved state: %#v", state))
	gomega.Expect(state.TaskID).To(gomega.Equal(brief.TaskID), fmt.Sprintf("unexpected saved state: %#v", state))
	gomega.Expect(state.EffortRoot).To(gomega.Equal(effortRoot), fmt.Sprintf("unexpected saved state: %#v", state))

	state.TaskID = "different-task-in-same-effort"
	_, err = featureplanner.SaveState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	_, err = defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "resume the already closed product task",
		Resume:                "closed-source-satisfied",
		MaxPlanningIterations: 3,
	}, io.Discard)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(
		fmt.Sprintf(`product-bound feature planner state task_id "different-task-in-same-effort" does not match product task_id %q`, brief.TaskID),
	)))

	state.TaskID = ""
	state.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: featureplanner.FeatureAtomPlan{ID: "existing-resumed-atom"},
		State:           featureplanner.AtomStatePlanned,
	}}
	_, err = featureplanner.SaveState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	_, err = defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "resume closed product state with missing task identity",
		Resume:                "closed-source-satisfied",
		MaxPlanningIterations: 3,
	}, io.Discard)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(
		"existing product-bound feature planner state is missing durable task_id",
	)))

})
var _ = ginkgo.It("default headless feature planner persists finalized all-source-satisfied delivery", ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"), func() {
	root := ginkgo.GinkgoT().TempDir()
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "source-producer-effort")
	writeFeaturePlannerResumeEffort(effortRoot, "source-producer-effort")
	writeFeaturePlannerFixtureFile(filepath.Join(root, "internal", "cmd", "featureplanner", "feature_planner.go"), "package featureplanner\n")
	writeFeaturePlannerFixtureFile(filepath.Join(root, "internal", "cmd", "featureplanner", "feature_planner_test.go"), "package featureplanner\n")
	gitInit := exec.Command("git", "init", "--quiet", root)
	gitOutput, err := gitInit.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git init: %s", gitOutput)

	originalModelTurn := runFeaturePlannerModelTurn
	originalPlanExecute := runFeaturePlannerPlanExecute
	ginkgo.DeferCleanup(func() {
		runFeaturePlannerModelTurn = originalModelTurn
		runFeaturePlannerPlanExecute = originalPlanExecute
	})

	modelCalls := 0
	planExecuteCalls := 0
	runFeaturePlannerModelTurn = func(_ context.Context, _ featurePlannerRequest, _ string, taskFamily string, _ io.Writer) (string, error) {
		modelCalls++
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerPlanner)))

		return featurePlannerResumeProductPlanJSON("source-producer-effort"), nil
	}
	runFeaturePlannerPlanExecute = func(_ context.Context, _ featurePlannerRequest, prompt string, _ io.Writer) (string, error) {
		planExecuteCalls++
		contract, ok := plancontract.PlanExecutionContractFromPrompt(prompt)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("plan-execute prompt missing typed execution contract:\n%s", prompt))
		gomega.Expect(contract.Atoms).To(gomega.HaveLen(1))
		paths := contract.Atoms[0].Scope.RequiredEditPaths()
		evidence := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(paths))
		for _, path := range paths {
			evidence = append(evidence, plancontract.SourceSatisfiedEvidenceRef{
				Path: path,
				Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
			})
		}
		body, err := json.Marshal(map[string]any{
			"tool":                            featureplannercontract.ToolPlanExecuteComplete,
			"lane_family":                     "plan-execute",
			"status":                          "source-satisfied",
			"phase":                           "complete",
			"terminal_class":                  "success",
			"outcome":                         "source_satisfied",
			"completion_source":               "durable_plan_execute_terminal_completion",
			"contract_id":                     contract.Atoms[0].Scope.ContractID,
			"snapshot_hash":                   contract.ContractHash,
			"required_edit_paths":             paths,
			"source_satisfied_evidence_paths": paths,
			"source_satisfied_evidence":       evidence,
			"acceptance_evidence_ids":         plancontract.RequiredAcceptanceEvidenceIDsFromPrompt(prompt),
			"acceptance_evidence_trusted":     true,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		return string(body), nil
	}

	result, err := defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root, CWD: root}},
		Prompt:                "confirm the product atom is already implemented",
		SessionID:             "source-producer-session",
		EffortRoot:            effortRoot,
		EffortID:              "source-producer-effort",
		MaxPlanningIterations: 3,
	}, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("produce source-satisfied delivery: %v", err))
	gomega.Expect(result.Fields["status"]).To(gomega.Equal(featureplanner.FeatureStateComplete), "result=%#v", result)
	gomega.Expect(modelCalls).To(gomega.Equal(1))
	gomega.Expect(planExecuteCalls).To(gomega.Equal(1))

	workflowState, err := productworkflow.LoadWorkflowState(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(workflowState.CurrentPhase).To(gomega.Equal(string(productworkflow.PhaseComplete)))
	gomega.Expect(workflowState.SourceSatisfiedDelivery).NotTo(gomega.BeNil())
	gomega.Expect(workflowState.SourceSatisfiedDelivery.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied))
	gomega.Expect(workflowState.SourceSatisfiedDelivery.EvidencePaths).To(gomega.ConsistOf(
		"internal/cmd/featureplanner/feature_planner.go",
		"internal/cmd/featureplanner/feature_planner_test.go",
	))

	handoff, err := productworkflow.LoadImplementationHandoff(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(handoff.SourceSatisfiedDelivery).NotTo(gomega.BeNil())
	gomega.Expect(handoff.SourceSatisfiedDelivery.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied))
	gomega.Expect(handoff.SourceSatisfiedDelivery.CompletionDigest).NotTo(gomega.BeEmpty())
})
var _ = ginkgo.It("default headless feature planner resume uses saved product context", func() {
	root := ginkgo.GinkgoT().TempDir()
	effortRoot := filepath.Join(root, "effort")
	writeFeaturePlannerResumeEffort(effortRoot, "resume-effort")

	originalModelTurn := runFeaturePlannerModelTurn
	originalPlanExecute := runFeaturePlannerPlanExecute
	ginkgo.DeferCleanup(func() {
		runFeaturePlannerModelTurn = originalModelTurn
		runFeaturePlannerPlanExecute = originalPlanExecute
	})

	modelCalls := 0

	var resumedPrompt string

	runFeaturePlannerModelTurn = func(_ context.Context, _ featurePlannerRequest, prompt string, taskFamily string, _ io.Writer) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerPlanner)), fmt.Sprintf("unexpected task family %q", taskFamily))

		modelCalls++
		if modelCalls == 1 {
			gomega.Expect(prompt).To(gomega.ContainSubstring("Product Workflow Context"), fmt.Sprintf("initial product run missing product context:\n%s", prompt))
			gomega.Expect(prompt).To(gomega.ContainSubstring("resume-effort"), fmt.Sprintf("initial product run missing product context:\n%s", prompt))

			return featurePlannerResumeProductPlanJSON("resume-effort"), nil
		}

		resumedPrompt = prompt

		return "<<QUESTION>>: resume prompt inspected", nil
	}
	runFeaturePlannerPlanExecute = func(_ context.Context, _ featurePlannerRequest, _ string, _ io.Writer) (string, error) {
		return `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","phase":"complete","completion_source":"test"}`, nil
	}

	_, err := defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "dispatch product atom",
		SessionID:             "resume-product",
		EffortRoot:            effortRoot,
		MaxPlanningIterations: 3,
	}, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("initial product run: %v", err))

	state, _, err := featureplanner.LoadState(root, "resume-product")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load state: %v", err))
	gomega.Expect(state.EffortRoot).To(gomega.Equal(effortRoot), fmt.Sprintf("product identity was not persisted: effort_root=%q effort_id=%q", state.EffortRoot, state.EffortID))
	gomega.Expect(state.EffortID).To(gomega.Equal("resume-effort"), fmt.Sprintf("product identity was not persisted: effort_root=%q effort_id=%q", state.EffortRoot, state.EffortID))

	_, err = defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "retry product atom",
		Resume:                "resume-product",
		MaxPlanningIterations: 3,
	}, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resume product run: %v", err))
	gomega.Expect(resumedPrompt).To(gomega.ContainSubstring("Product Workflow Context"), fmt.Sprintf("resumed run should reload saved product workflow context, got:\n%s", resumedPrompt))
	gomega.Expect(resumedPrompt).To(gomega.ContainSubstring("resume-effort"), fmt.Sprintf("resumed run should reload saved product workflow context, got:\n%s", resumedPrompt))

})
var _ = ginkgo.It("default headless feature planner resume fails closed with missing saved product context", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := featureplanner.NewState("resume-product", "original product atom", "", 3, time.Unix(1, 0).UTC())

	state.EffortID = "quality-contributor-breakdown"
	state.EffortRoot = filepath.Join(root, "missing-effort")

	if _, err := featureplanner.SaveState(root, &state); err != nil {
		ginkgo.Fail(fmt.Sprintf("save state: %v", err))
	}

	originalModelTurn := runFeaturePlannerModelTurn
	ginkgo.DeferCleanup(func() { runFeaturePlannerModelTurn = originalModelTurn })

	runFeaturePlannerModelTurn = func(_ context.Context, _ featurePlannerRequest, _ string, _ string, _ io.Writer) (string, error) {
		ginkgo.Fail("resumed product workflow state must not fall back to standalone planning when saved effort context cannot load")

		return "", nil
	}

	_, err := defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "retry the atom",
		Resume:                "resume-product",
		MaxPlanningIterations: 3,
	}, io.Discard)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("product workflow context unavailable")), fmt.Sprintf("expected saved product context error, got %v", err))

})
var _ = ginkgo.It("default headless feature planner resume rejects mismatched product context", func() {
	root := ginkgo.GinkgoT().TempDir()
	effortRoot := filepath.Join(root, "effort")
	writeFeaturePlannerResumeEffort(effortRoot, "loaded-effort")

	state := featureplanner.NewState("resume-mismatch", "original product atom", "", 3, time.Unix(1, 0).UTC())

	state.EffortID = "saved-effort"
	state.EffortRoot = effortRoot

	if _, err := featureplanner.SaveState(root, &state); err != nil {
		ginkgo.Fail(fmt.Sprintf("save state: %v", err))
	}

	originalModelTurn := runFeaturePlannerModelTurn
	ginkgo.DeferCleanup(func() { runFeaturePlannerModelTurn = originalModelTurn })

	runFeaturePlannerModelTurn = func(_ context.Context, _ featurePlannerRequest, _ string, _ string, _ io.Writer) (string, error) {
		ginkgo.Fail("mismatched product context must fail before planner model call")

		return "", nil
	}

	_, err := defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "retry product atom",
		Resume:                "resume-mismatch",
		MaxPlanningIterations: 3,
	}, io.Discard)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("product workflow context mismatch")), fmt.Sprintf("expected product context mismatch, got %v", err))

})

func testContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func featurePlannerPlanExecuteRunner(run func(coreplanexecute.Options, io.Writer, io.Writer) int) PlanExecuteRunner {
	return func(_ config.Config, opts coreplanexecute.Options, stdout io.Writer, stderr io.Writer) coreplanexecute.RunResult {
		return coreplanexecute.RunResult{ExitCode: run(opts, stdout, stderr)}
	}
}

func featurePlannerPlanExecuteDispatchPlanJSON() string {
	return `{
  "tool": "submit_feature_plan",
  "schema_version": "idiom.feature_plan.v1",
  "feature_id": "feature-plan-execute-dispatch",
  "source_task": {"kind": "test", "summary": "Route feature-planner atom execution through plan-execute"},
  "summary": "Route atom execution through plan-execute",
  "top_level_scope": {
    "objective": "Exercise feature-planner atom dispatch",
    "success_criteria": ["the atom execution callback is plan-execute"]
  },
  "sdd_contract": {
    "refs": ["SDD-PlanExecuteDispatch"],
    "acceptance": ["ready atoms execute through plan-execute"]
  },
  "tdd_contract": {
    "coverage_tier": "contract_high",
    "required_before_implementation": true,
    "obligations": [
      {
        "id": "TDD-PlanExecuteDispatch",
        "behavior": "feature-planner dispatch delegates code writing to plan-execute",
        "command": "go test ./internal/surfaces/cli/featureplanner -ginkgo.focus='default headless feature planner dispatches atoms through plan execute' -ginkgo.fail-on-empty",
        "required_before": "implementation"
      }
    ]
  },
  "behavioral_coverage_draft": {
    "authority": "standalone_provisional",
    "status": "draft",
    "tier": "contract_high",
    "requirements": [
      {
        "id": "REQ-PlanExecuteDispatch",
        "statement": "Ready feature-planner atoms execute through plan-execute.",
        "tdd_obligations": ["TDD-PlanExecuteDispatch"]
      }
    ],
    "scenarios": [
      {
        "id": "BDD-PlanExecuteDispatch",
        "requirement_id": "REQ-PlanExecuteDispatch",
        "given": ["a standalone feature plan with one ready atom"],
        "when": ["the headless feature-planner dispatch loop runs"],
        "then": ["the atom implementation prompt is sent to plan-execute"],
        "concrete_data": {
          "required_edit_path": "docs/feature-planner-plan-execute.txt",
          "test_path": "cmd/idiom/feature_planner_test.go"
        },
        "obligations": ["TDD-PlanExecuteDispatch"]
      }
    ],
    "invariants": [
      {
        "id": "INV-PlanExecuteDispatch",
        "statement": "Feature-planner must not directly model-call implementation atoms when plan-execute dispatch is configured.",
        "property_based_test_refs": ["default headless feature planner dispatches atoms through plan execute"],
        "obligations": ["TDD-PlanExecuteDispatch"]
      }
    ],
    "surface_inventory": [
      {
        "id": "SURF-PlanExecuteDispatch",
        "surface": "workflow_dispatch",
        "status": "in_scope",
        "rationale": "the requested behavior is feature-planner dispatch through plan-execute",
        "tdd_obligations": ["TDD-PlanExecuteDispatch"]
      },
      {
        "id": "SURF-PlanExecuteTransport",
        "surface": "api_transport",
        "status": "not_required",
        "rationale": "no external API or transport boundary changes in this dispatch fixture"
      }
    ],
    "test_obligations": [
      {
        "id": "TDD-PlanExecuteDispatch",
        "behavior": "feature-planner dispatch delegates code writing to plan-execute",
        "command": "go test ./internal/surfaces/cli/featureplanner -ginkgo.focus='default headless feature planner dispatches atoms through plan execute' -ginkgo.fail-on-empty",
        "type": "integration"
      }
    ],
    "verification": [
      {
        "id": "VERIFY-PlanExecuteDispatch",
        "gate": "plan-execute dispatch fixture passes",
        "command": "go test ./internal/surfaces/cli/featureplanner -ginkgo.focus='default headless feature planner dispatches atoms through plan execute' -ginkgo.fail-on-empty"
      }
    ]
  },
  "atoms": [
    {
      "schema_version": "idiom.feature_atom.v1",
      "feature_id": "feature-plan-execute-dispatch",
      "id": "atom-plan-execute-dispatch",
      "title": "Dispatch through plan-execute",
      "summary": "Prove feature-planner does not directly model-call implementation atoms",
      "required_edit_paths": ["docs/feature-planner-plan-execute.txt", "cmd/idiom/feature_planner_test.go"],
      "sdd_refs": ["SDD-PlanExecuteDispatch"],
      "tdd_obligations": ["TDD-PlanExecuteDispatch"],
      "required_tests": ["go test ./internal/surfaces/cli/featureplanner -ginkgo.focus='default headless feature planner dispatches atoms through plan execute' -ginkgo.fail-on-empty"],
      "test_first": {
        "obligation_refs": ["TDD-PlanExecuteDispatch"],
        "commands": ["go test ./internal/surfaces/cli/featureplanner -ginkgo.focus='default headless feature planner dispatches atoms through plan execute' -ginkgo.fail-on-empty"],
        "baseline_required": true,
        "test_artifact_paths": ["cmd/idiom/feature_planner_test.go"],
        "pre_execution_event_ids": ["evt-plan-execute-dispatch"],
        "expected_pre_execution_result": "red-confirmed",
        "coverage_gate_event_id": "evt-plan-execute-dispatch"
      },
      "planned_tdd_snapshot": {
        "snapshot_id": "snapshot-plan-execute-dispatch",
        "snapshot_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        "obligation_ids": ["TDD-PlanExecuteDispatch"],
        "test_commands": ["go test ./internal/surfaces/cli/featureplanner -ginkgo.focus='default headless feature planner dispatches atoms through plan execute' -ginkgo.fail-on-empty"],
        "test_artifact_paths": ["cmd/idiom/feature_planner_test.go"],
        "expected_pre_execution_results": ["red-confirmed"],
        "created_before_dispatch_event_id": "evt-plan-execute-dispatch"
      },
      "prompt": "Update docs/feature-planner-plan-execute.txt and keep the atom bounded."
    }
  ]
}`
}

func featurePlannerPlanExecuteDispatchPlanJSONWithEvidenceOnlySurface() string {

	var payload map[string]any
	gomega.Expect(json.Unmarshal([]byte(featurePlannerPlanExecuteDispatchPlanJSON()), &payload)).To(gomega.Succeed())

	draft := payload["behavioral_coverage_draft"].(map[string]any)
	surfaces := draft["surface_inventory"].([]any)
	surface := surfaces[0].(map[string]any)
	delete(surface, "tdd_obligations")
	surface["evidence_refs"] = []any{"docs/feature-planner-plan-execute.txt"}

	body, err := json.Marshal(payload)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal dispatch fixture: %v", err))

	return string(body)
}

func writeMainConfig() string {

	root := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))

	path := filepath.Join(root, "config.json")
	gomega.Expect(os.WriteFile(path, []byte("{}\n"), 0o644)).To(gomega.Succeed())

	return path
}

func writeMainConfigRoot(root string) string {
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	gomega.Expect(os.WriteFile(path, []byte(configJSON+"\n"), 0o644)).To(gomega.Succeed())

	return path
}

func writeFeaturePlannerFixtureFile(path string, content string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())

}

var _ = ginkgo.Describe("Feature-planner product workflow context", ginkgo.Label("integration"), func() {
	ginkgo.It("injects artifact IDs into the planner prompt", func() {
		brief := &featureplanner.ProductWorkflowBrief{
			EffortID:   "test-fixture-effort",
			TaskID:     "test-task",
			EffortRoot: "testdata/feature-planner-pw-integration/effort",
			PRDRequirements: []featureplanner.PRDRequirementBrief{
				{ID: "REQ-001", Priority: "P0", Statement: "Add a thing that works",
					Testability: "automated", Obligations: []string{"TDD-1"}},
			},
			AcceptedADRIDs:   []string{"ADR-001"},
			AcceptedADRPaths: []string{"adrs/001-test.md"},
			TDDObligations: []featureplanner.TDDObligationBrief{
				{ID: "TDD-1", Behavior: "Thing works", Command: "go test ./...",
					RequiredBefore: "implementation", Status: "open"},
			},
			CoverageTier:       "behavior_standard",
			CoverageGatePassed: true,
			FilesToModify:      []string{"internal/example/thing.go"},
			Tests:              []string{"go test ./internal/example/..."},
			ArtifactRootPath:   "testdata/feature-planner-pw-integration/effort",
		}

		state := featureplanner.NewState("test-session", "build feature", "", 3, time.Unix(1, 0).UTC())
		prompt := featureplanner.BuildPlannerPrompt("Add a thing", state, brief)

		for _, expected := range []string{"REQ-001", "ADR-001", "TDD-1", "Product Workflow Context", "idiom.feature_plan.v2"} {
			gomega.Expect(prompt).To(gomega.ContainSubstring(expected))
		}
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring("docs/path.md"))
	})
})

func writeFeaturePlannerResumeEffort(root string, effortID string) {
	gomega.Expect(os.MkdirAll(filepath.Join(root, "adrs"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(filepath.Join(root, "briefs"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(filepath.Join(root, "evidence"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(filepath.Join(root, "reports"), 0o755)).To(gomega.Succeed())

	writeFeaturePlannerJSON(filepath.Join(root, "workflow_state.json"), productworkflow.WorkflowState{
		SchemaVersion:    "idiom.product_workflow_state.v1",
		EffortID:         effortID,
		TaskID:           "resume-task",
		Title:            "Resume product context",
		StatusBucket:     "started",
		CurrentPhase:     string(productworkflow.PhaseTDDSynthesis),
		CurrentState:     productworkflow.WorkflowStatePlanned,
		StateRevision:    1,
		LastEventID:      "evt-resume-fixture-ready",
		ArtifactPaths:    map[string]string{"events": "events.jsonl"},
		Coverage:         productworkflow.CoverageState{CoverageTier: "unit"},
		Privacy:          productworkflow.PrivacyState{RedactionStatus: "applied"},
		FolderPath:       root,
		NextValidActions: []string{"finalize"},
		DurabilityLevel:  "source_controlled",
		Context: productworkflow.WorkflowContext{
			ContextPackID:   "pack-resume-context",
			FreshnessState:  "current",
			ReferencedPaths: []string{"internal/cmd/featureplanner/feature_planner.go", "internal/cmd/featureplanner/feature_planner_test.go"},
			RefreshPolicy:   "on_demand",
		},
	})
	gomega.Expect(os.WriteFile(filepath.Join(root, "events.jsonl"), nil, 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "briefs", "resume.md"), []byte("# Resume implementation brief\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "reports", "resume.json"), []byte("{}\n"), 0o644)).To(gomega.Succeed())

	prd := productworkflow.PRDDocument{
		FrontMatter: productworkflow.PRDFrontMatter{
			SchemaVersion:     "idiom.prd.v1",
			PRDID:             "PRD-RESUME",
			Status:            "accepted",
			TaskID:            "resume-task",
			CreatedAt:         "2026-06-07T00:00:00Z",
			AcceptedAt:        "2026-06-07T00:01:00Z",
			AcceptedBy:        "test",
			AcceptanceProfile: "human-review",
		},
		Requirements: []productworkflow.PRDRequirement{{
			ID:             "REQ-RESUME",
			Priority:       "must",
			Statement:      "Resume product workflow context for feature-planner atoms",
			EvidenceRefs:   []string{"EV-RESUME"},
			TDDObligations: []string{"TDD-RESUME"},
			OwnerAlignment: "operator-approved",
			Testability:    "go test ./internal/cmd/featureplanner",
			Metric:         "resumed planner prompt contains product context",
		}},
	}
	gomega.Expect(os.WriteFile(filepath.Join(root, "prd.md"), []byte(productworkflow.RenderPRDMarkdown(prd)), 0o644)).To(gomega.Succeed())

	writeFeaturePlannerJSON(filepath.Join(root, "tdd.json"), productworkflow.TDDLedger{
		SchemaVersion: "idiom.tdd_ledger.v1",
		EffortID:      effortID,
		TaskID:        "resume-task",
		CoverageTier:  "unit",
		Coverage:      productworkflow.TDDCoverage{MutationMinScore: 1},
		Obligations: []productworkflow.TDDObligation{{
			ID:             "TDD-RESUME",
			Type:           "regression",
			Behavior:       "Feature-planner resume carries saved product context",
			Owner:          "internal/cmd/featureplanner/feature_planner_test.go",
			Command:        "go test ./internal/cmd/featureplanner",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	})
	writeFeaturePlannerJSON(filepath.Join(root, "adrs", "index.json"), productworkflow.ADRIndex{
		SchemaVersion: "idiom.adr_index.v1",
		EffortID:      effortID,
		ADRs: []productworkflow.ADRIndexEntry{{
			ADRID:        "ADR-RESUME",
			Title:        "Resume product context",
			Status:       "accepted",
			Path:         "ADR-RESUME.md",
			CoverageTier: "unit",
			PRDRefs:      []string{"REQ-RESUME"},
		}},
	})

	adr := productworkflow.ADRDocument{
		FrontMatter: productworkflow.ADRFrontMatter{
			SchemaVersion:     "idiom.adr.v1",
			ADRID:             "ADR-RESUME",
			Status:            "accepted",
			PRDRefs:           []string{"REQ-RESUME"},
			EvidenceRefs:      []string{"EV-RESUME"},
			TDDObligations:    []string{"TDD-RESUME"},
			CoverageTier:      "unit",
			AcceptedAt:        "2026-06-07T00:02:00Z",
			AcceptedBy:        "test",
			AcceptanceProfile: "human-review",
		},
		BodyMarkdown: strings.Join([]string{
			"## Context",
			"Resumed product workflow atoms need durable context.",
			"## Decision",
			"Use saved effort_root when resuming feature-planner state.",
			"## Alternatives Considered",
			"Fall back to standalone planning.",
			"## Evidence",
			"EV-RESUME",
			"## Consequences",
			"Resumed atoms keep product scope authority.",
			"## Compatibility and Migration",
			"Existing standalone runs remain standalone.",
			"## TDD Obligations",
			"TDD-RESUME",
			"## Rollback",
			"Restore explicit-only effort root handling.",
			"## Open Questions",
			"None.",
		}, "\n\n"),
	}
	gomega.Expect(os.WriteFile(filepath.Join(root, "adrs", "ADR-RESUME.md"), []byte(productworkflow.RenderADRMarkdown(adr)), 0o644)).To(gomega.Succeed())

	writeFeaturePlannerJSON(filepath.Join(root, "evidence", "traceability_matrix.json"), productworkflow.TraceabilityMatrix{
		SchemaVersion: "idiom.traceability_matrix.v1",
		EffortID:      effortID,
		TaskID:        "resume-task",
		CreatedAt:     "2026-06-07T00:03:00Z",
		Matrix: []productworkflow.TraceabilityRow{{
			EvidenceCard:   "EV-RESUME",
			PRDRequirement: "REQ-RESUME",
			ADR:            "ADR-RESUME",
			TDDObligation:  "TDD-RESUME",
			Brief:          "briefs/resume.md",
			Report:         "reports/resume.json",
		}},
	})

	ledger := []productworkflow.ImplementationLedgerRecord{
		{
			RecordType:           productworkflow.LedgerRecordAtomPlanned,
			TaskID:               "resume-task",
			EffortID:             effortID,
			Timestamp:            "2026-06-07T00:04:00Z",
			ComplexityTier:       "C1",
			ContextPackID:        "pack-resume-context",
			Implementor:          &productworkflow.ImplementorAssignment{ID: "impl-resume", Runtime: "idiom", Model: "test-model", Command: "idiom plan-execute"},
			Reviewer:             &productworkflow.ReviewerAssignment{ID: "review-resume", Runtime: "codex", Model: "review-model", ReasoningEffort: "medium"},
			Scope:                &productworkflow.AtomScope{ExpectedFiles: []string{"internal/cmd/featureplanner/feature_planner.go", "internal/cmd/featureplanner/feature_planner_test.go"}, ForbiddenPatterns: []string{"docs/product-workflows/**"}},
			RequiredVerification: []string{"go test ./internal/cmd/featureplanner"},
			ReportPaths:          &productworkflow.ReportPaths{Implementation: "reports/resume-implementation.json", Review: "reports/resume-review.json", Investigation: "reports/resume-investigation.json"},
		},
		{
			RecordType:              productworkflow.LedgerRecordVerificationCompleted,
			TaskID:                  "resume-task",
			EffortID:                effortID,
			Timestamp:               "2026-06-07T00:05:00Z",
			TDDObligationsSatisfied: []string{"TDD-RESUME"},
			VerificationStatus:      "pass",
			VerificationCommandsRun: []string{"go test ./internal/cmd/featureplanner"},
		},
	}

	var ledgerLines []byte

	for _, record := range ledger {
		data, err := json.Marshal(record)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal ledger: %v", err))

		ledgerLines = append(ledgerLines, data...)
		ledgerLines = append(ledgerLines, '\n')
	}
	gomega.Expect(os.WriteFile(filepath.Join(root, "implementation_ledger.jsonl"), ledgerLines, 0o644)).To(gomega.Succeed())

}

func writeFeaturePlannerJSON(path string, value any) {

	data, err := json.MarshalIndent(value, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal %s: %v", path, err))
	gomega.Expect(os.WriteFile(path, append(data, '\n'), 0o644)).To(gomega.Succeed())

}

func featurePlannerResumeProductPlanJSON(effortID string) string {
	return fmt.Sprintf(`{
  "tool": "submit_feature_plan",
  "schema_version": "idiom.feature_plan.v2",
  "feature_id": %q,
  "source_task": {"kind": "test", "summary": "Persist and reload product context on feature-planner resume"},
  "summary": "Persist and reload product context on feature-planner resume",
  "top_level_scope": {
    "objective": "Keep product workflow context authoritative across feature-planner resume",
    "success_criteria": ["resumed planner prompt contains product workflow context"]
  },
  "sdd_contract": {
    "acceptance": ["product context is preserved across resume"],
    "prd_requirement_refs": ["REQ-RESUME"],
    "adr_refs": ["ADR-RESUME"]
  },
  "tdd_contract": {
    "coverage_tier": "unit",
    "required_before_implementation": true,
    "obligations": [
      {
        "id": "TDD-RESUME",
        "behavior": "Feature-planner resume carries saved product context",
        "command": "go test ./internal/cmd/featureplanner",
        "required_before": "implementation"
      }
    ]
  },
  "atoms": [
    {
      "schema_version": "idiom.feature_atom.v1",
      "feature_id": %q,
      "id": "atom-resume-context",
      "title": "Resume Product Context",
      "summary": "Prove feature-planner persists and reloads product workflow context",
      "required_edit_paths": [
        "internal/cmd/featureplanner/feature_planner.go",
        "internal/cmd/featureplanner/feature_planner_test.go"
      ],
      "sdd_refs": ["REQ-RESUME", "ADR-RESUME"],
      "tdd_obligations": ["TDD-RESUME"],
      "required_tests": ["go test ./internal/cmd/featureplanner"],
      "test_first": {
        "obligation_refs": ["TDD-RESUME"],
        "commands": ["go test ./internal/cmd/featureplanner"],
        "baseline_required": true,
        "test_artifact_paths": ["internal/cmd/featureplanner/feature_planner_test.go"],
        "pre_execution_event_ids": ["evt-resume-context"],
        "expected_pre_execution_result": "red-confirmed",
        "coverage_gate_event_id": "evt-resume-context"
      },
      "planned_tdd_snapshot": {
        "snapshot_id": "snapshot-resume-context",
        "snapshot_digest": "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
        "obligation_ids": ["TDD-RESUME"],
        "test_commands": ["go test ./internal/cmd/featureplanner"],
        "test_artifact_paths": ["internal/cmd/featureplanner/feature_planner_test.go"],
        "expected_pre_execution_results": ["red-confirmed"],
        "created_before_dispatch_event_id": "evt-resume-context"
      },
      "prompt": "Keep the product workflow context authoritative across resume."
    }
  ]
}`, effortID, effortID)
}

var _ = ginkgo.Describe("Feature-planner product workflow gates", ginkgo.Label("integration"), func() {
	ginkgo.It("blocks planning when the coverage gate has not passed", func() {
		brief := &featureplanner.ProductWorkflowBrief{
			EffortID:           "test-effort",
			CoverageTier:       "contract_high",
			CoverageGatePassed: false,
		}

		state := featureplanner.NewState("test-session", "build feature", "", 3, time.Unix(1, 0).UTC())
		prompt := featureplanner.BuildPlannerPrompt("Add a thing", state, brief)

		gomega.Expect(prompt).To(gomega.ContainSubstring("coverage gate has NOT passed"))
		gomega.Expect(prompt).To(gomega.ContainSubstring("ask_planner_question"))
	})

	ginkgo.It("allows dispatch when only TDD verification evidence is pending", func() {
		brief := &featureplanner.ProductWorkflowBrief{
			EffortID:           "test-effort",
			CoverageTier:       "contract_high",
			CoverageGatePassed: false,
			CoverageBlockers:   []string{"expected all 1 TDD obligations to have verification evidence"},
			TDDObligations: []featureplanner.TDDObligationBrief{{
				ID:             "TDD-1",
				Behavior:       "prove the feature",
				Command:        "go test ./...",
				RequiredBefore: "implementation",
				Status:         "planned",
			}},
			FilesToModify: []string{"internal/labels/labels.go"},
			Tests:         []string{"go test ./..."},
		}

		state := featureplanner.NewState("test-session", "build feature", "", 3, time.Unix(1, 0).UTC())
		prompt := featureplanner.BuildPlannerPrompt("Add a thing", state, brief)

		gomega.Expect(prompt).To(gomega.ContainSubstring("Required submit_feature_plan shape"))
		gomega.Expect(prompt).To(gomega.ContainSubstring("pending only because TDD pre-execution evidence is not recorded yet"))
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring("Do not produce a feature plan"))
	})

	ginkgo.It("uses acceptance criteria in standalone mode without placeholder refs", func() {
		state := featureplanner.NewState("test-session", "build feature", "", 3, time.Unix(1, 0).UTC())
		prompt := featureplanner.BuildPlannerPrompt("Add a thing", state, nil)

		gomega.Expect(prompt).NotTo(gomega.ContainSubstring(`"refs":["docs/path.md:line"]`))
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring(`"sdd_refs":["docs/path.md:line"]`))
		gomega.Expect(prompt).To(gomega.ContainSubstring("acceptance criteria"))
	})

	ginkgo.It("round-trips product workflow context through JSON", func() {
		brief := featureplanner.ProductWorkflowBrief{
			EffortID: "test-effort",
			PRDRequirements: []featureplanner.PRDRequirementBrief{
				{ID: "REQ-001", Priority: "P0", Statement: "Do the thing",
					Testability: "automated", Obligations: []string{"TDD-1"}},
			},
			AcceptedADRIDs: []string{"ADR-001"},
			TDDObligations: []featureplanner.TDDObligationBrief{
				{ID: "TDD-1", Behavior: "test passes", Command: "go test ./...",
					RequiredBefore: "implementation", Status: "open"},
			},
			CoverageTier:       "behavior_standard",
			CoverageGatePassed: true,
		}

		data, err := json.Marshal(brief)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var roundTripped featureplanner.ProductWorkflowBrief
		gomega.Expect(json.Unmarshal(data, &roundTripped)).To(gomega.Succeed())
		gomega.Expect(roundTripped.EffortID).To(gomega.Equal("test-effort"))
		gomega.Expect(roundTripped.PRDRequirements).To(gomega.HaveLen(1))
		gomega.Expect(roundTripped.PRDRequirements[0].ID).To(gomega.Equal("REQ-001"))
	})
})
var _ = ginkgo.It("emit feature planner json event rejects unknown contract event", func() {
	var stdout bytes.Buffer

	if err := emitFeaturePlannerJSONEvent(&stdout, true, featureplannercontract.FeaturePlannerEventType("not_real"), nil); err == nil {
		ginkgo.Fail("expected unknown event type error")
	}
	gomega.Expect(stdout.Len()).To(gomega.Equal(0), fmt.Sprintf("unexpected JSON output for rejected event: %s", stdout.String()))

})
