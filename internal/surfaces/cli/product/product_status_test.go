package product

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/workflows/productworkflow"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.It("product status obsidian parity failure sections", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, ".git/HEAD", "ref: refs/heads/main\n")
	writeTestFile(t, dir, "workflow_state.json", `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"prd-adr-brief-workflow",
		"task_id":"impl-0032-phase-5-obsidian-parity-check",
		"title":"Test Effort",
		"status_bucket":"started",
		"current_phase":"phase-5-execution-feedback",
		"current_state":"planned",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["complete"]
	}`)
	writeTestFile(t, dir, productworkflow.ObsidianProductWorkflowPath, "> **Status: NOT STARTED**")
	writeTestFile(t, dir, productworkflow.ObsidianRecoveryPath, "# Recovery and Decomposition")
	writeTestFile(t, dir, "reports/impl-0032-investigation.json", validTestTestInvestigationReportJSON())

	var jsonStdout, jsonStderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected status JSON to fail on Obsidian parity, got code=%d stdout=%s stderr=%s", code, jsonStdout.String(), jsonStderr.String())

	var view productworkflow.ProductStatusView

	{
		err := json.Unmarshal(jsonStdout.Bytes(), &view)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "status output should be valid JSON: %v\n%s", err, jsonStdout.String())
	}

	found := false

	for _, sec := range view.Sections {
		if sec.Name == "obsidian" {
			found = true

			gomega.Expect(sec.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusFail), "expected failing obsidian section, got %#v", sec)
			gomega.Expect(sec.FindingCount).NotTo(gomega.Equal(0), "expected failing obsidian section, got %#v", sec)
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected obsidian section in status JSON, got %#v", view.Sections)

	var textStdout, textStderr bytes.Buffer

	code = runProductStatus([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected status text to fail on Obsidian parity, got code=%d stdout=%s stderr=%s", code, textStdout.String(), textStderr.String())

	gomega.Expect(textStdout.String()).To(gomega.ContainSubstring("obsidian: fail"), "expected text output to include failing obsidian section, got:\n%s", textStdout.String())
	gomega.Expect(textStdout.String()).To(gomega.ContainSubstring("findings"), "expected text output to include failing obsidian section, got:\n%s", textStdout.String())
})

var _ = ginkgo.It("product status json on current valid fixture", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)

	ws := loadProductWorkflowFixtureState(t, effortRoot)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected status to pass on current valid fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["effort_id"]).To(gomega.BeEquivalentTo(ws["effort_id"]), "expected effort_id %v, got %v", ws["effort_id"], result["effort_id"])

	gomega.Expect(result["task_id"]).To(gomega.BeEquivalentTo(ws["task_id"]), "expected task_id %v, got %v", ws["task_id"], result["task_id"])

	gomega.Expect(result["current_state"]).To(gomega.BeEquivalentTo(ws["current_state"]), "expected current_state %v, got %v", ws["current_state"], result["current_state"])

	ctx, _ := ws["context"].(map[string]any)
	gomega.Expect(result["context_pack_id"]).To(gomega.BeEquivalentTo(ctx["context_pack_id"]), "expected context_pack_id %v, got %v", ctx["context_pack_id"], result["context_pack_id"])

	requiredFields := []string{
		"audit_status", "effort_root", "effort_id", "task_id",
		"status_bucket", "current_phase", "current_state",
		"state_revision", "last_event_id", "context_pack_id",
		"freshness_state", "checked_file_count", "sections",
		"blockers", "open_obligations", "next_valid_actions", "metrics",
	}
	for _, field := range requiredFields {
		{
			_, ok := result[field]
			gomega.Expect(ok).To(gomega.BeTrue(), "expected field %q in JSON output", field)
		}
	}

	{
		_, ok := result["operator"]
		gomega.Expect(ok).To(gomega.BeFalse(), "default status JSON must not include operator view: %#v", result["operator"])
	}
})

var _ = ginkgo.It("product status metrics", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductStatusView

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Metrics).NotTo(gomega.BeNil(), "expected metrics in product status JSON")

	gomega.Expect(result.Metrics.CurrentPhase).NotTo(gomega.BeEmpty(), "expected metrics phase/state, got %#v", result.Metrics)
	gomega.Expect(result.Metrics.CurrentState).NotTo(gomega.BeEmpty(), "expected metrics phase/state, got %#v", result.Metrics)
})

var _ = ginkgo.It("product status metrics fails closed on unknown gate status", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())
	writeTestFile(t, dir, "workflow_state.json", strings.Replace(
		validTestWorkflowStateJSON(),
		`"coverage":{"coverage_tier":"unit"}`,
		`"coverage":{"coverage_tier":"unit","gate_results":[{"gate":"metrics","status":"surprise"}]}`,
		1,
	))

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected status to fail closed, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductStatusView

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.AuditStatus).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected audit_status fail, got %q", result.AuditStatus)

	metricsSection := findStatusSection(t, result.Sections, "metrics")
	gomega.Expect(metricsSection.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusFail), "expected metrics section failure, got %#v", metricsSection)
	gomega.Expect(metricsSection.FindingCount).To(gomega.Equal(1), "expected metrics section failure, got %#v", metricsSection)

	gomega.Expect(result.Metrics).To(gomega.BeNil(), "expected metrics omitted on failure, got %#v", result.Metrics)
})

var _ = ginkgo.It("product status text includes key fields", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)

	ws := loadProductWorkflowFixtureState(t, effortRoot)
	effortID, _ := ws["effort_id"].(string)
	currentState, _ := ws["current_state"].(string)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", effortRoot}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected status to pass on current valid fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	output := stdout.String()
	for _, want := range []string{effortID, currentState, "audit:", "next_actions:", "metrics: phase=", "next:"} {
		gomega.Expect(strings.Contains(output, want)).To(gomega.BeTrue(), "expected text output to contain %q, got:\n%s", want, output)
	}

	gomega.Expect(strings.Contains(output, "continuation_actions")).To(gomega.BeFalse(), "default status text must not include operator continuation actions, got:\n%s", output)
})

var _ = ginkgo.It("product status operator view json", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-1"
		state.NextValidActions = []string{"submit_for_human_verification"}
	})

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--operator-view", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected operator status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductOperatorStatusView

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "operator output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.SchemaVersion).To(gomega.BeEquivalentTo("idiom.product_operator_status.v1"), "expected operator schema version, got %q", result.SchemaVersion)

	gomega.Expect(result.ContinuationAuthority).To(gomega.BeEquivalentTo("workflow_state"), "expected workflow_state continuation authority, got %q", result.ContinuationAuthority)

	gomega.Expect(result.ArtifactPaths).To(gomega.HaveLen(1), "expected artifact paths from workflow_state, got %#v", result.ArtifactPaths)
	gomega.Expect(result.ArtifactPaths[0].Name).To(gomega.Equal("events"), "expected artifact paths from workflow_state, got %#v", result.ArtifactPaths)
	gomega.Expect(result.ArtifactPaths[0].Path).To(gomega.Equal("events.jsonl"), "expected artifact paths from workflow_state, got %#v", result.ArtifactPaths)

	gomega.Expect(len(result.NextValidActions)).To(gomega.BeEquivalentTo(1), "expected typed continuation actions for planned state, got %#v", result.NextValidActions)

	var action productworkflow.ProductOperatorAction

	for _, candidate := range result.NextValidActions {
		if candidate.ID == "submit_for_human_verification" {
			action = candidate
			break
		}
	}

	gomega.Expect(action.ID).To(gomega.Equal("submit_for_human_verification"), "expected typed human-verification transition action, got %#v", action)
	gomega.Expect(action.Label).To(gomega.Equal("submit for human verification"), "expected typed human-verification transition action, got %#v", action)
	gomega.Expect(action.Owner).To(gomega.Equal("planner"), "expected typed human-verification transition action, got %#v", action)
	gomega.Expect(action.Dispatchable).To(gomega.BeTrue(), "expected typed human-verification transition action, got %#v", action)
	gomega.Expect(action.RunnableNow).To(gomega.BeTrue(), "expected typed human-verification transition action, got %#v", action)
})

var _ = ginkgo.It("product create initializes effort", func() {
	t := ginkgo.GinkgoT()
	dir := filepath.Join(t.TempDir(), "new-effort")

	var stdout, stderr bytes.Buffer

	code := runProductCreate([]string{"--effort-root", dir, "--effort-id", "new-effort", "--title", "New Effort", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected create to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.CreateResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "create output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusOK), "expected new PRD drafting effort, got %#v", result)
	gomega.Expect(result.EffortID).To(gomega.Equal("new-effort"), "expected new PRD drafting effort, got %#v", result)
	gomega.Expect(result.CurrentPhase).To(gomega.Equal(productworkflow.PhasePRDDrafting), "expected new PRD drafting effort, got %#v", result)

	state, err := productworkflow.LoadWorkflowState(dir)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "load created workflow state: %v", err)

	gomega.Expect(state.EffortID).To(gomega.Equal("new-effort"), "unexpected created workflow state: %#v", state)
	gomega.Expect(state.Title).To(gomega.Equal("New Effort"), "unexpected created workflow state: %#v", state)
	gomega.Expect(state.CurrentState).To(gomega.Equal(productworkflow.WorkflowStatePlanned), "unexpected created workflow state: %#v", state)

	for _, relPath := range []string{"adrs", "evidence", "briefs", "reports"} {
		info, err := os.Stat(filepath.Join(dir, relPath))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected created directory %s: %v", relPath, err)

		gomega.Expect(info.IsDir()).To(gomega.BeTrue(), "expected %s to be a directory", relPath)
	}
})

var _ = ginkgo.It("product transition applies workflow action", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-1"
		state.NextValidActions = []string{"submit_for_human_verification"}
	})

	var stdout, stderr bytes.Buffer

	code := runProductTransition([]string{"--effort-root", dir, "--action", "submit_for_human_verification", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected transition to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductWorkflowTransitionApplyResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "transition output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusApplied), "expected applied planned -> human_verification result, got %#v", result)
	gomega.Expect(result.FromState).To(gomega.Equal("planned"), "expected applied planned -> human_verification result, got %#v", result)
	gomega.Expect(result.ToState).To(gomega.Equal("human_verification"), "expected applied planned -> human_verification result, got %#v", result)

	state, err := productworkflow.LoadWorkflowState(dir)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "load updated workflow state: %v", err)

	gomega.Expect(state.CurrentState).To(gomega.Equal("human_verification"), "expected updated workflow state with approval action, got %#v", state)
	gomega.Expect(containsString(state.NextValidActions, "approve_for_implementation")).To(gomega.BeTrue(), "expected updated workflow state with approval action, got %#v", state)

	events, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read transition events: %v", err)

	gomega.Expect(string(events)).To(gomega.ContainSubstring(`"event":"product_workflow_transition_applied"`), "expected transition event in events.jsonl, got:\n%s", string(events))
	gomega.Expect(string(events)).To(gomega.ContainSubstring(`"action":"submit_for_human_verification"`), "expected transition event in events.jsonl, got:\n%s", string(events))
})

var _ = ginkgo.It("product transition blocks unlisted action without mutation", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-1"
		state.NextValidActions = []string{"submit_for_human_verification"}
	})

	beforeState, err := os.ReadFile(filepath.Join(dir, "workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read workflow state before transition: %v", err)

	beforeEvents, beforeEventsExists := readOptionalTestFile(t, filepath.Join(dir, "events.jsonl"))

	var stdout, stderr bytes.Buffer

	code := runProductTransition([]string{"--effort-root", dir, "--action", "approve_for_implementation"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked transition exit code 1, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(strings.Contains(stdout.String(), "action_not_listed")).To(gomega.BeTrue(), "expected action_not_listed blocker, got:\n%s", stdout.String())

	afterState, err := os.ReadFile(filepath.Join(dir, "workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read workflow state after transition: %v", err)

	afterEvents, afterEventsExists := readOptionalTestFile(t, filepath.Join(dir, "events.jsonl"))

	gomega.Expect(string(afterState)).To(gomega.BeEquivalentTo(string(beforeState)), "blocked transition mutated workflow_state.json\nbefore:\n%s\n\nafter:\n%s", string(beforeState), string(afterState))

	gomega.Expect(afterEventsExists).To(gomega.Equal(beforeEventsExists), "blocked transition mutated events.jsonl\nbefore:\n%s\n\nafter:\n%s", string(beforeEvents), string(afterEvents))
	gomega.Expect(string(afterEvents)).To(gomega.Equal(string(beforeEvents)), "blocked transition mutated events.jsonl\nbefore:\n%s\n\nafter:\n%s", string(beforeEvents), string(afterEvents))
})

var _ = ginkgo.It("product status operator view blocks legacy start implementation without human verification", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-1"
		state.NextValidActions = []string{"start_implementation"}
	})

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--operator-view", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected operator status to preserve status exit behavior, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductOperatorStatusView

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "operator output should be valid JSON: %v\n%s", err, stdout.String())
	}

	action := findProductOperatorAction(t, result.NextValidActions, "start_implementation")
	gomega.Expect(action.Dispatchable).To(gomega.BeFalse(), "legacy start_implementation must be blocked until human verification, got %#v", action)
	gomega.Expect(action.RunnableNow).To(gomega.BeFalse(), "legacy start_implementation must be blocked until human verification, got %#v", action)

	gomega.Expect(containsString(action.BlockedBy, "human_verification_required")).To(gomega.BeTrue(), "expected human_verification_required blocker, got %#v", action)
})

var _ = ginkgo.It("product status operator view includes recovery packet", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/recovery_packet.json", productWorkflowTestRecoveryPacketJSON(t))

	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.ArtifactPaths["recovery_packet"] = "references/recovery_packet.json"
	})

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--operator-view", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected operator status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductOperatorStatusView

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "operator output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Recovery).NotTo(gomega.BeNil(), "expected recovery status projection, got %#v", result.Recovery)
	gomega.Expect(result.Recovery.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusReady), "expected recovery status projection, got %#v", result.Recovery)
	gomega.Expect(result.Recovery.CandidateAtomCount).To(gomega.Equal(1), "expected recovery status projection, got %#v", result.Recovery)

	workflowAction := findProductOperatorAction(t, result.NextValidActions, "finalize")
	gomega.Expect(workflowAction.Dispatchable).To(gomega.BeFalse(), "expected workflow action blocked while recovery is open, got %#v", workflowAction)
	gomega.Expect(workflowAction.RunnableNow).To(gomega.BeFalse(), "expected workflow action blocked while recovery is open, got %#v", workflowAction)

	recoveryAction := findProductOperatorAction(t, result.NextValidActions, "recovery_review_decomposition")
	gomega.Expect(recoveryAction.Dispatchable).To(gomega.BeFalse(), "expected runnable recovery inspect action, got %#v", recoveryAction)
	gomega.Expect(recoveryAction.RunnableNow).To(gomega.BeTrue(), "expected runnable recovery inspect action, got %#v", recoveryAction)
	gomega.Expect(recoveryAction.CommandHint).To(gomega.Equal("idiom recover status --packet references/recovery_packet.json"), "expected runnable recovery inspect action, got %#v", recoveryAction)

	stdout.Reset()
	stderr.Reset()

	code = runProductStatus([]string{"--effort-root", dir, "--operator-view"}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected operator status text to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	text := stdout.String()
	for _, want := range []string{
		"recovery: ready next=review_decomposition candidate_atoms=1 packet=references/recovery_packet.json",
		"[recovery_packet_open] review recovery packet before continuing workflow dispatch",
		"command_hint=idiom recover status --packet references/recovery_packet.json",
	} {
		gomega.Expect(strings.Contains(text, want)).To(gomega.BeTrue(), "expected operator recovery text to contain %q, got:\n%s", want, text)
	}
})

var _ = ginkgo.It("product status operator view text fails closed on stale context", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-1"
		state.Context.FreshnessState = "stale"
		state.NextValidActions = []string{"submit_for_human_verification"}
	})

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--operator-view"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected status exit code to preserve existing behavior, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	text := stdout.String()
	for _, want := range []string{
		"product operator status:",
		"authority: workflow_state",
		"[stale_context] workflow freshness_state \"stale\" is not current",
		"next_valid_actions (1):",
		"dispatchable=false",
		"runnable_now=false",
		"blocked_by=stale_context",
	} {
		gomega.Expect(strings.Contains(text, want)).To(gomega.BeTrue(), "expected operator text to contain %q, got:\n%s", want, text)
	}
})

var _ = ginkgo.It("product status operator view uses workflow actions on audit fail", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--operator-view", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected operator status audit fail code 1, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductOperatorStatusView

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "operator audit-fail output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(len(result.NextValidActions)).To(gomega.BeEquivalentTo(1), "expected durable workflow action only, got %#v", result.NextValidActions)

	action := result.NextValidActions[0]
	gomega.Expect(action.ID).To(gomega.Equal("finalize"), "expected operator view to preserve workflow_state next action, got %#v", action)
	gomega.Expect(action.Action).To(gomega.Equal("finalize"), "expected operator view to preserve workflow_state next action, got %#v", action)

	gomega.Expect(action.Dispatchable).To(gomega.BeFalse(), "expected audit failure to block workflow action, got %#v", action)
	gomega.Expect(action.RunnableNow).To(gomega.BeFalse(), "expected audit failure to block workflow action, got %#v", action)

	gomega.Expect(strings.Contains(strings.Join(action.BlockedBy, ","), "audit_not_passing")).To(gomega.BeTrue(), "expected audit_not_passing blocker, got %#v", action)

	gomega.Expect(action.Action).NotTo(gomega.ContainSubstring("fix product workflow findings"), "operator view must not use CLI remediation prose as continuation authority, got %#v", action)
	gomega.Expect(action.ID).NotTo(gomega.ContainSubstring("fix_product_workflow_findings"), "operator view must not use CLI remediation prose as continuation authority, got %#v", action)
})

var _ = ginkgo.It("product status json does not include operator continuation view", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var raw map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &raw)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "status JSON should decode to raw map: %v\n%s", err, stdout.String())
	}

	{
		_, ok := raw["operator"]
		gomega.Expect(ok).To(gomega.BeFalse(), "default status JSON must not include operator view: %s", stdout.String())
	}
})

var _ = ginkgo.It("product status operator no runtime mutation", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)

	before, err := os.ReadFile(filepath.Join(dir, "workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read workflow_state.json before status: %v", err)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	after, err := os.ReadFile(filepath.Join(dir, "workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read workflow_state.json after status: %v", err)

	gomega.Expect(string(after)).To(gomega.BeEquivalentTo(string(before)), "product status must not mutate workflow_state.json")

	{
		_, err := os.Stat(filepath.Join(dir, ".idiom"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "product status must not create .idiom runtime state, err=%v", err)
	}
})

var _ = ginkgo.Describe("product status text output", func() {
	ginkgo.It("is actionable for a passing effort", func() {
		t := ginkgo.GinkgoT()
		effortRoot := writeValidTestEffort(t)

		var jsonStdout, jsonStderr bytes.Buffer

		jsonCode := runProductStatus([]string{"--effort-root", effortRoot, "--json"}, &jsonStdout, &jsonStderr, nil)

		gomega.Expect(jsonCode).To(gomega.BeEquivalentTo(0), "expected pass status code 0, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())

		var out productworkflow.ProductStatusView

		{
			err := json.Unmarshal(jsonStdout.Bytes(), &out)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected valid status JSON: %v", err)
		}

		gomega.Expect(out.NextValidActions).NotTo(gomega.BeEmpty(), "expected next_valid_actions for pass output, got %#v", out.NextValidActions)
		gomega.Expect(strings.TrimSpace(out.NextValidActions[0])).NotTo(gomega.BeEmpty(), "expected next_valid_actions for pass output, got %#v", out.NextValidActions)

		var textStdout, textStderr bytes.Buffer

		textCode := runProductStatus([]string{"--effort-root", effortRoot}, &textStdout, &textStderr, nil)

		gomega.Expect(textCode).To(gomega.BeEquivalentTo(0), "expected pass text code 0, got code=%d stdout=%s stderr=%s", textCode, textStdout.String(), textStderr.String())

		text := textStdout.String()
		gomega.Expect(strings.Contains(text, "  status: OK")).To(gomega.BeTrue(), "expected deterministic OK status label, got:\n%s", text)

		gomega.Expect(strings.Contains(text, "  next: "+out.NextValidActions[0])).To(gomega.BeTrue(), "expected text next action parity with JSON value %q, got:\n%s", out.NextValidActions[0], text)
	})

	ginkgo.It("is actionable when the audit fails", func() {
		t := ginkgo.GinkgoT()
		dir := writeValidTestEffort(t)
		writeTestFile(t, dir, "tdd.json", `{not valid json`)

		var jsonStdout, jsonStderr bytes.Buffer

		jsonCode := runProductStatus([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

		gomega.Expect(jsonCode).To(gomega.BeEquivalentTo(1), "expected audit-fail status code 1, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())

		var out productworkflow.ProductStatusView

		{
			err := json.Unmarshal(jsonStdout.Bytes(), &out)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected valid status JSON: %v", err)
		}

		gomega.Expect(out.NextValidActions).NotTo(gomega.BeEmpty(), "expected next_valid_actions for fail output, got %#v", out.NextValidActions)
		gomega.Expect(strings.TrimSpace(out.NextValidActions[0])).NotTo(gomega.BeEmpty(), "expected next_valid_actions for fail output, got %#v", out.NextValidActions)

		gomega.Expect(strings.Contains(out.NextValidActions[0], "fix product workflow findings")).To(gomega.BeTrue(), "expected current failure remediation as first next action, got %#v", out.NextValidActions)

		gomega.Expect(out.NextValidActions[0]).NotTo(gomega.BeEquivalentTo("complete"), "expected not to surface stale workflow progression as first next action, got %#v", out.NextValidActions)

		var textStdout, textStderr bytes.Buffer

		textCode := runProductStatus([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

		gomega.Expect(textCode).To(gomega.BeEquivalentTo(1), "expected audit-fail text code 1, got code=%d stdout=%s stderr=%s", textCode, textStdout.String(), textStderr.String())

		text := textStdout.String()
		gomega.Expect(strings.Contains(text, "  status: FAILED")).To(gomega.BeTrue(), "expected deterministic FAILED status label, got:\n%s", text)

		gomega.Expect(strings.Contains(text, "    [metrics_error] ")).To(gomega.BeTrue(), "expected typed blocker identity in text output, got:\n%s", text)

		gomega.Expect(strings.Contains(text, "  next: "+out.NextValidActions[0])).To(gomega.BeTrue(), "expected text next action parity with JSON value %q, got:\n%s", out.NextValidActions[0], text)
	})

	ginkgo.It("is actionable when workflow state is missing", func() {
		t := ginkgo.GinkgoT()
		dir := t.TempDir()

		var jsonStdout, jsonStderr bytes.Buffer

		jsonCode := runProductStatus([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

		gomega.Expect(jsonCode).To(gomega.BeEquivalentTo(1), "expected error status code 1, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())

		var out productworkflow.ProductStatusView

		{
			err := json.Unmarshal(jsonStdout.Bytes(), &out)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected valid status JSON: %v", err)
		}

		gomega.Expect(out.NextValidActions).NotTo(gomega.BeEmpty(), "expected next_valid_actions for error output, got %#v", out.NextValidActions)
		gomega.Expect(strings.TrimSpace(out.NextValidActions[0])).NotTo(gomega.BeEmpty(), "expected next_valid_actions for error output, got %#v", out.NextValidActions)

		var textStdout, textStderr bytes.Buffer

		textCode := runProductStatus([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

		gomega.Expect(textCode).To(gomega.BeEquivalentTo(1), "expected error text code 1, got code=%d stdout=%s stderr=%s", textCode, textStdout.String(), textStderr.String())

		text := textStdout.String()
		gomega.Expect(strings.Contains(text, dir)).To(gomega.BeFalse(), "expected privacy-safe blocker details, got absolute path in text output:\n%s", text)

		gomega.Expect(strings.Contains(text, "  status: ERROR")).To(gomega.BeTrue(), "expected deterministic ERROR status label, got:\n%s", text)

		gomega.Expect(strings.Contains(text, "  [missing_workflow_state] workflow_state.json not found")).To(gomega.BeTrue(), "expected typed blocker identity in text output, got:\n%s", text)

		gomega.Expect(strings.Contains(text, "  next: "+out.NextValidActions[0])).To(gomega.BeTrue(), "expected text next action parity with JSON value %q, got:\n%s", out.NextValidActions[0], text)
	})
})

var _ = ginkgo.It("product status error next valid actions parity", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var jsonStdout, jsonStderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected error code, got %d", code)

	var jsonResult productworkflow.ProductStatusView

	{
		err := json.Unmarshal(jsonStdout.Bytes(), &jsonResult)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "invalid json: %v", err)
	}

	gomega.Expect(len(jsonResult.NextValidActions)).NotTo(gomega.BeEquivalentTo(0), "expected next_valid_actions for status error path")

	var textStdout, textStderr bytes.Buffer

	code = runProductStatus([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected error text code, got %d", code)

	gomega.Expect(strings.Contains(textStdout.String(), "next: "+jsonResult.NextValidActions[0])).To(gomega.BeTrue(), "expected text next-action parity, got:\n%s", textStdout.String())
})

var _ = ginkgo.It("product status audit fail text includes next action", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit fail code, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(strings.Contains(stdout.String(), "next: fix product workflow findings")).To(gomega.BeTrue(), "expected actionable next line, got:\n%s", stdout.String())

	gomega.Expect(strings.Contains(stdout.String(), "next: complete")).To(gomega.BeFalse(), "expected status failure not to surface stale workflow progression, got:\n%s", stdout.String())
})

var _ = ginkgo.It("product status missing workflow state exits1", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for missing workflow_state.json, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "JSON output should be valid even on error: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["audit_status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusError), "expected audit_status error, got %v", result["audit_status"])

	blockers, ok := result["blockers"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected at least one blocker")
	gomega.Expect(blockers).NotTo(gomega.BeEmpty(), "expected at least one blocker")
})

var _ = ginkgo.It("product status invalid workflow state exits1", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", `{not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for invalid workflow_state.json, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "JSON output must be valid even on parse error: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["audit_status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusError), "expected audit_status error, got %v", result["audit_status"])
})

var _ = ginkgo.It("product status semantic invalid workflow state exits1", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", `{"schema_version":"idiom.product_workflow_state.v0.fixture"}`)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for semantically invalid workflow_state.json, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "JSON output must be valid even on validation error: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["audit_status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusError), "expected audit_status error, got %v", result["audit_status"])

	blockers, ok := result["blockers"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected at least one blocker")
	gomega.Expect(blockers).NotTo(gomega.BeEmpty(), "expected at least one blocker")

	firstBlocker, ok := blockers[0].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected blocker to be a map")

	gomega.Expect(firstBlocker["code"]).To(gomega.BeEquivalentTo("invalid_workflow_state"), "expected blocker code invalid_workflow_state, got %v", firstBlocker["code"])
})

var _ = ginkgo.It("product efforts json lists filters and summarizes", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	writeTestFile(t, effortsRoot, filepath.Join("started", "beta", "workflow_state.json"), productEffortsCLIWorkflowState("beta-effort", "Beta Work", "started", "implementing", "beta"))
	writeTestFile(t, effortsRoot, filepath.Join("unstarted", "alpha", "workflow_state.json"), productEffortsCLIWorkflowState("alpha-effort", "Alpha Work", "unstarted", "planned", "alpha"))

	var stdout, stderr bytes.Buffer

	code := runProductEfforts([]string{"--root", effortsRoot, "--status", "started", "--query", "beta", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected efforts to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductEffortsResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected one matching effort, got %#v", result)
	gomega.Expect(result.Counts.EffortCount).To(gomega.Equal(1), "expected one matching effort, got %#v", result)
	gomega.Expect(result.Efforts).To(gomega.HaveLen(1), "expected one matching effort, got %#v", result)

	effort := result.Efforts[0]
	gomega.Expect(effort.EffortID).To(gomega.Equal("beta-effort"), "unexpected effort summary: %#v", effort)
	gomega.Expect(effort.RelativeFolderPath).To(gomega.Equal("started/beta"), "unexpected effort summary: %#v", effort)

	gomega.Expect(effort.ContextPackID).To(gomega.Equal("pack-beta-effort"), "expected context summary, got %#v", effort)
	gomega.Expect(effort.FreshnessState).To(gomega.Equal("current"), "expected context summary, got %#v", effort)

	gomega.Expect(effort.BlockerCount).To(gomega.Equal(1), "expected blocker and obligation counts, got %#v", effort)
	gomega.Expect(effort.OpenObligationCount).To(gomega.Equal(1), "expected blocker and obligation counts, got %#v", effort)

	gomega.Expect(effort.NextValidActions).To(gomega.HaveLen(1), "expected next action summary, got %#v", effort.NextValidActions)
	gomega.Expect(effort.NextValidActions[0]).To(gomega.Equal("submit_for_verification"), "expected next action summary, got %#v", effort.NextValidActions)
})

var _ = ginkgo.It("product efforts text reports typed workflow state findings", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")

	{
		err := os.MkdirAll(filepath.Join(effortsRoot, "started", "missing"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	}

	writeTestFile(t, effortsRoot, filepath.Join("started", "bad", "workflow_state.json"), `{not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductEfforts([]string{"--root", effortsRoot, "--status", "started"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected malformed effort state to fail, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	out := stdout.String()
	for _, want := range []string{
		"product efforts: efforts FAILED",
		"[invalid_workflow_state:started/bad/workflow_state.json]",
		"[missing_workflow_state:started/missing/workflow_state.json]",
		"next: fix product effort workflow_state findings before relying on the effort browser",
	} {
		gomega.Expect(strings.Contains(out, want)).To(gomega.BeTrue(), "expected text output to contain %q, got:\n%s", want, out)
	}
})

var _ = ginkgo.It("product efforts text includes full summary fields", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	writeTestFile(t, effortsRoot, filepath.Join("started", "beta", "workflow_state.json"), productEffortsCLIWorkflowState("beta-effort", "Beta Work", "started", "implementing", "beta"))

	var stdout, stderr bytes.Buffer

	code := runProductEfforts([]string{"--root", effortsRoot}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected efforts to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	out := stdout.String()
	for _, want := range []string{
		"title=\"Beta Work\"",
		"last_event=evt-007",
		"context_pack=pack-beta-effort",
		"freshness=current",
		"next_actions: submit_for_verification",
	} {
		gomega.Expect(strings.Contains(out, want)).To(gomega.BeTrue(), "expected text output to contain %q, got:\n%s", want, out)
	}
})

var _ = ginkgo.It("product efforts usage errors are typed", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := runProductEfforts([]string{"--root", root, "--status", "later", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected unknown status usage error, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductEffortsResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusFail), "expected typed invalid status finding, got %#v", result)
	gomega.Expect(result.Findings).To(gomega.HaveLen(1), "expected typed invalid status finding, got %#v", result)
	gomega.Expect(result.Findings[0].Code).To(gomega.Equal("invalid_status_filter"), "expected typed invalid status finding, got %#v", result)

	stdout.Reset()
	stderr.Reset()

	code = runProductEfforts([]string{"--root", filepath.Join("..", "efforts"), "--json"}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected unsafe root usage error, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Findings).To(gomega.HaveLen(1), "expected unsafe root finding, got %#v", result)
	gomega.Expect(result.Findings[0].Code).To(gomega.Equal("unsafe_efforts_root"), "expected unsafe root finding, got %#v", result)

	stdout.Reset()
	stderr.Reset()

	code = runProductEfforts([]string{"--root", root, "extra"}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected stray positional argument usage error, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(strings.Contains(stderr.String(), "unexpected arguments: extra")).To(gomega.BeTrue(), "expected unexpected argument message, got stderr=%s", stderr.String())
})

var _ = ginkgo.It("product status migration apply json plan is read only", func() {
	t := ginkgo.GinkgoT()
	effortsRoot, effortRoot := writeProductStatusMigrationCLIFixture(t, "started", "reviewing")

	beforeWorkflow, err := os.ReadFile(filepath.Join(effortRoot, "workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read workflow_state.json before plan: %v", err)

	beforeEvents, err := os.ReadFile(filepath.Join(effortRoot, "events.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read events.jsonl before plan: %v", err)

	beforeIndex, err := os.ReadFile(filepath.Join(effortsRoot, "index.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read index.json before plan: %v", err)

	var stdout, stderr bytes.Buffer

	code := runProductStatusMigration([]string{"--effort-root", effortRoot, "--target-status", "closed", "--plan", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected plan success, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductStatusMigrationPlan

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "plan output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPlanned), "expected planned CLI result, got %#v", result)

	gomega.Expect(result.SourceFolderPath).To(gomega.Equal("started/test-effort"), "unexpected CLI plan paths: %#v", result)
	gomega.Expect(result.TargetFolderPath).To(gomega.Equal("closed/test-effort"), "unexpected CLI plan paths: %#v", result)

	gomega.Expect(result.Event.EventID).NotTo(gomega.BeEmpty(), "expected CLI plan event and revision, got %#v", result)
	gomega.Expect(result.Event.Details.StateRevision).To(gomega.Equal(8), "expected CLI plan event and revision, got %#v", result)

	afterWorkflow, err := os.ReadFile(filepath.Join(effortRoot, "workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read workflow_state.json after plan: %v", err)

	afterEvents, err := os.ReadFile(filepath.Join(effortRoot, "events.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read events.jsonl after plan: %v", err)

	afterIndex, err := os.ReadFile(filepath.Join(effortsRoot, "index.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read index.json after plan: %v", err)

	gomega.Expect(string(beforeWorkflow)).To(gomega.BeEquivalentTo(string(afterWorkflow)), "CLI plan must not mutate workflow_state.json")

	gomega.Expect(string(beforeEvents)).To(gomega.BeEquivalentTo(string(afterEvents)), "CLI plan must not mutate events.jsonl")

	gomega.Expect(string(beforeIndex)).To(gomega.BeEquivalentTo(string(afterIndex)), "CLI plan must not mutate index.json")
})

var _ = ginkgo.It("product status migration apply json apply moves effort", func() {
	t := ginkgo.GinkgoT()
	effortsRoot, effortRoot := writeProductStatusMigrationCLIFixture(t, "started", "reviewing")

	var stdout, stderr bytes.Buffer

	code := runProductStatusMigration([]string{
		"--effort-root", effortRoot,
		"--target-status", "closed",
		"--apply",
		"--expected-revision", "7",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected apply success, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductStatusMigrationPlan

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "apply output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusApplied), "expected applied CLI result, got %#v", result)

	gomega.Expect(result.Event.Event).To(gomega.Equal("status_migration_applied"), "unexpected CLI apply event payload: %#v", result.Event)
	gomega.Expect(result.Event.EventID).NotTo(gomega.BeEmpty(), "unexpected CLI apply event payload: %#v", result.Event)

	targetRoot := filepath.Join(effortsRoot, "closed", "test-effort")

	{
		_, err := os.Stat(effortRoot)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected CLI apply to move source effort folder, stat err=%v", err)
	}

	workflowData, err := os.ReadFile(filepath.Join(targetRoot, "workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read migrated workflow_state.json: %v", err)

	ws, err := productworkflow.ParseWorkflowStateJSON(workflowData)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "parse migrated workflow_state.json: %v", err)

	gomega.Expect(ws.StatusBucket).To(gomega.Equal("closed"), "unexpected migrated workflow state: %#v", ws)
	gomega.Expect(ws.CurrentState).To(gomega.Equal("closed"), "unexpected migrated workflow state: %#v", ws)
	gomega.Expect(ws.StateRevision).To(gomega.Equal(8), "unexpected migrated workflow state: %#v", ws)

	gomega.Expect(ws.LastEventID).To(gomega.BeEquivalentTo(result.Event.EventID), "expected workflow_state last_event_id %q, got %q", result.Event.EventID, ws.LastEventID)
})

var _ = ginkgo.Describe("product status migration apply usage errors", func() {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unsafe traversal effort root",
			args: []string{
				"--effort-root", filepath.Join("..", "efforts", "started", "test-effort"),
				"--target-status", "closed",
				"--plan",
				"--json",
			},
			want: "unsafe_effort_root",
		},
		{
			name: "missing effort root",
			args: []string{
				"--target-status", "closed",
				"--plan",
				"--json",
			},
			want: "missing_effort_root",
		},
		{
			name: "missing target status",
			args: []string{
				"--effort-root", filepath.Join("started", "test-effort"),
				"--plan",
				"--json",
			},
			want: "missing_target_status",
		},
		{
			name: "mode conflict",
			args: []string{
				"--effort-root", filepath.Join("started", "test-effort"),
				"--target-status", "closed",
				"--plan",
				"--apply",
				"--json",
			},
			want: "invalid_migration_mode",
		},
		{
			name: "apply missing expected revision",
			args: []string{
				"--effort-root", filepath.Join("started", "test-effort"),
				"--target-status", "closed",
				"--apply",
				"--json",
			},
			want: "missing_expected_revision",
		},
		{
			name: "unknown flag",
			args: []string{
				"--json",
				"--bogus",
			},
			want: "invalid_arguments",
		},
		{
			name: "malformed expected revision",
			args: []string{
				"--effort-root", filepath.Join("started", "test-effort"),
				"--target-status", "closed",
				"--apply",
				"--expected-revision", "not-a-number",
				"--json",
			},
			want: "invalid_arguments",
		},
		{
			name: "json true malformed expected revision",
			args: []string{
				"--json=true",
				"--effort-root", filepath.Join("started", "test-effort"),
				"--target-status", "closed",
				"--apply",
				"--expected-revision", "not-a-number",
			},
			want: "invalid_arguments",
		},
	}

	for _, tc := range cases {
		tc := tc
		ginkgo.It(tc.name, func() {
			var stdout, stderr bytes.Buffer

			code := runProductStatusMigration(tc.args, &stdout, &stderr, nil)

			gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected typed usage error code 2, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

			var result productworkflow.ProductStatusMigrationPlan

			{
				err := json.Unmarshal(stdout.Bytes(), &result)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), "usage error output should still be valid JSON: %v\n%s", err, stdout.String())
			}

			gomega.Expect(result.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusBlocked), "expected %s blocker, got %#v", tc.want, result)
			gomega.Expect(result.Blockers).To(gomega.HaveLen(1), "expected %s blocker, got %#v", tc.want, result)
			gomega.Expect(result.Blockers[0].Code).To(gomega.Equal(tc.want), "expected %s blocker, got %#v", tc.want, result)
		})
	}
})

var _ = ginkgo.It("product status usage errors", func() {
	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 for missing --effort-root, got %d", code)

	gomega.Expect(stdout.Len()).To(gomega.BeEquivalentTo(0), "expected no stdout for text usage error, got %q", stdout.String())

	gomega.Expect(strings.Contains(stderr.String(), "product status: --effort-root is required")).To(gomega.BeTrue(), "expected missing effort root usage error, got %q", stderr.String())
})

var _ = ginkgo.It("product status json usage errors are typed", func() {
	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 for JSON missing --effort-root, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(strings.TrimSpace(stderr.String())).To(gomega.BeEquivalentTo(""), "expected JSON usage error to keep stderr empty, got %q", stderr.String())

	var result productStatusUsageJSONResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "usage error output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status with one blocker, got %#v", result)
	gomega.Expect(result.Blockers).To(gomega.HaveLen(1), "expected blocked status with one blocker, got %#v", result)

	blocker := result.Blockers[0]
	gomega.Expect(blocker.Code).To(gomega.Equal("missing-argument"), "unexpected usage blocker: %#v", blocker)
	gomega.Expect(blocker.Path).To(gomega.Equal("--effort-root"), "unexpected usage blocker: %#v", blocker)
	gomega.Expect(blocker.Message).To(gomega.Equal("--effort-root is required"), "unexpected usage blocker: %#v", blocker)

	gomega.Expect(strings.Contains(result.NextValidAction, "--effort-root")).To(gomega.BeTrue(), "expected next valid action to mention --effort-root, got %q", result.NextValidAction)
})

var _ = ginkgo.It("product status help", func() {
	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"-h"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0 for -h, got %d", code)
})

var _ = ginkgo.It("product status returns code 1 when the audit fails", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 when audit_status != pass, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "JSON output must be valid even on audit fail: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["audit_status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected audit_status fail, got %v", result["audit_status"])

	findings, ok := result["findings"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected findings in failed status output")
	gomega.Expect(findings).NotTo(gomega.BeEmpty(), "expected findings in failed status output")

	metricErr := false

	for _, raw := range findings {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if entry["code"] == "metrics_error" {
			metricErr = true
			break
		}
	}

	gomega.Expect(metricErr).To(gomega.BeTrue(), "expected metrics_error finding in status output, got %#v", findings)
})

var _ = ginkgo.It("product status usage includes status", func() {
	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"--help"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0 for help, got %d", code)

	gomega.Expect(strings.Contains(stdout.String(), "status")).To(gomega.BeTrue(), "expected usage to mention status, got:\n%s", stdout.String())
})
