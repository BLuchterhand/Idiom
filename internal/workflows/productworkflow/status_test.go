package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"

	"idiom/internal/contracts/workflowstate"
	"idiom/internal/workflows/recovery"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("status", func() {
	ginkgo.It("LoadProductStatusMissingWorkflowState", func() {
		dir := ginkgo.GinkgoT().TempDir()

		view, err := LoadProductStatus(dir, nil)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected error for missing workflow_state.json")
		gomega.Expect(view.AuditStatus != "error").To(gomega.BeFalse(), fmt.Sprintf("expected audit_status error, got %q", view.AuditStatus))
		gomega.Expect(view.WorkflowID).To(gomega.Equal(workflowstate.WorkflowProductDispatch), fmt.Sprintf("missing workflow state should project missing-scope terminal state, got %#v", view))
		gomega.Expect(view.WorkflowState).To(gomega.Equal(workflowstate.StateTerminal), fmt.Sprintf("missing workflow state should project missing-scope terminal state, got %#v", view))
		gomega.Expect(view.WorkflowTerminalState).To(gomega.Equal(workflowstate.TerminalStateMissingScope), fmt.Sprintf("missing workflow state should project missing-scope terminal state, got %#v", view))
		gomega.Expect(view.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("missing workflow state should project missing-scope terminal state, got %#v", view))
		gomega.Expect(view.Blockers).NotTo(gomega.BeEmpty(), "expected at least one blocker")
		gomega.Expect(view.Blockers[0].Code != "missing_workflow_state").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code missing_workflow_state, got %q", view.Blockers[0].Code))
		gomega.Expect(view.Blockers[0].Message != "workflow_state.json not found").To(gomega.BeFalse(), fmt.Sprintf("expected privacy-safe missing workflow_state message, got %q", view.Blockers[0].Message))
		gomega.Expect(view.NextValidActions).To(gomega.HaveLen(1), fmt.Sprintf("expected missing workflow_state recovery action, got %#v", view.NextValidActions))
		gomega.Expect(view.NextValidActions[0]).To(gomega.Equal("restore workflow_state.json under the effort root and rerun product status"), fmt.Sprintf("expected missing workflow_state recovery action, got %#v", view.NextValidActions))
		gomega.Expect(containsStatusSubstring(view, dir)).To(gomega.BeFalse(), fmt.Sprintf("status error view leaked absolute effort path %q: %#v", dir, view))

		operatorView := BuildProductOperatorStatusView(view, nil)
		gomega.Expect(len(operatorView.NextValidActions) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected shared operator recovery view, got %#v", operatorView))

		action := operatorView.NextValidActions[0]
		gomega.Expect(action.Owner).To(gomega.Equal("operator"), fmt.Sprintf("expected non-dispatchable runnable recovery action, got %#v", action))
		gomega.Expect(action.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("expected non-dispatchable runnable recovery action, got %#v", action))
		gomega.Expect(action.RunnableNow).To(gomega.BeTrue(), fmt.Sprintf("expected non-dispatchable runnable recovery action, got %#v", action))
		gomega.Expect(operatorView.WorkflowID).To(gomega.Equal(workflowstate.WorkflowProductDispatch), fmt.Sprintf("operator error view should preserve product workflow terminal projection, got %#v", operatorView))
		gomega.Expect(operatorView.WorkflowState).To(gomega.Equal(workflowstate.StateTerminal), fmt.Sprintf("operator error view should preserve product workflow terminal projection, got %#v", operatorView))
		gomega.Expect(operatorView.WorkflowTerminalState).To(gomega.Equal(workflowstate.TerminalStateMissingScope), fmt.Sprintf("operator error view should preserve product workflow terminal projection, got %#v", operatorView))
		gomega.Expect(operatorView.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("operator error view should preserve product workflow terminal projection, got %#v", operatorView))

	})
	ginkgo.It("LoadProductStatusInvalidWorkflowStateJSON", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeStatusTestFile(dir, "workflow_state.json", `{not valid json`)

		view, err := LoadProductStatus(dir, nil)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected error for invalid workflow_state.json")
		gomega.Expect(view.AuditStatus != "error").To(gomega.BeFalse(), fmt.Sprintf("expected audit_status error, got %q", view.AuditStatus))
		gomega.Expect(view.Blockers).NotTo(gomega.BeEmpty(), "expected at least one blocker")
		gomega.Expect(view.Blockers[0].Code != "invalid_workflow_state").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_workflow_state, got %q", view.Blockers[0].Code))
		gomega.Expect(view.NextValidActions).To(gomega.HaveLen(1), fmt.Sprintf("expected invalid workflow_state recovery action, got %#v", view.NextValidActions))
		gomega.Expect(view.NextValidActions[0]).To(gomega.Equal("fix workflow_state.json content and rerun product status"), fmt.Sprintf("expected invalid workflow_state recovery action, got %#v", view.NextValidActions))
		gomega.Expect(containsStatusSubstring(view, dir)).To(gomega.BeFalse(), fmt.Sprintf("invalid workflow_state view leaked absolute effort path %q: %#v", dir, view))

		out, marshalErr := json.Marshal(view)
		gomega.Expect(marshalErr != nil).To(gomega.BeFalse(), fmt.Sprintf("error view must be valid JSON: %v", marshalErr))

		var parsed map[string]any
		{

			jsonErr := json.Unmarshal(out, &parsed)
			gomega.Expect(jsonErr != nil).To(gomega.BeFalse(), fmt.Sprintf("error view must round-trip as JSON: %v", jsonErr))
		}

	})
	ginkgo.It("ProductStatusDisplayNextValidActions", func() {
		tests := []struct {
			name             string
			view             ProductStatusView
			workflowStateErr bool
			want             []string
		}{
			{
				name:             "workflow state load error uses existing recovery action",
				view:             ProductStatusView{NextValidActions: []string{"fix workflow_state.json content and rerun product status"}},
				workflowStateErr: true,
				want:             []string{"fix workflow_state.json content and rerun product status"},
			},
			{
				name:             "workflow state load error supplies recovery fallback",
				view:             ProductStatusView{},
				workflowStateErr: true,
				want:             []string{"restore a valid workflow_state.json, then rerun `idiom product status --effort-root <path>`"},
			},
			{
				name: "audit failure prepends remediation",
				view: ProductStatusView{
					AuditStatus:      ProductWorkflowStatusFail,
					NextValidActions: []string{"submit_for_verification"},
				},
				want: []string{ProductStatusAuditRemediationAction, "submit_for_verification"},
			},
			{
				name: "pass keeps workflow actions",
				view: ProductStatusView{
					AuditStatus:      ProductWorkflowStatusPass,
					NextValidActions: []string{"complete"},
				},
				want: []string{"complete"},
			},
			{
				name: "pass supplies audit fallback",
				view: ProductStatusView{AuditStatus: ProductWorkflowStatusPass},
				want: []string{"run `idiom product audit --effort-root <path>` for detailed section findings"},
			},
		}

		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				got := ProductStatusDisplayNextValidActions(tt.view, tt.workflowStateErr)
				gomega.Expect(!sameStatusTestStrings(got, tt.want)).To(gomega.BeFalse(), fmt.Sprintf("expected %#v, got %#v", tt.want, got))

			})
		}
	})
	ginkgo.It("LoadProductStatusSemanticInvalidWorkflowState", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeStatusTestFile(dir, "workflow_state.json", `{"schema_version":"idiom.product_workflow_state.v0.fixture"}`)

		view, err := LoadProductStatus(dir, nil)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected error for semantically invalid workflow_state.json")
		gomega.Expect(view.AuditStatus != "error").To(gomega.BeFalse(), fmt.Sprintf("expected audit_status error, got %q", view.AuditStatus))
		gomega.Expect(view.Blockers).NotTo(gomega.BeEmpty(), "expected at least one blocker")
		gomega.Expect(view.Blockers[0].Code != "invalid_workflow_state").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_workflow_state, got %q", view.Blockers[0].Code))

		out, marshalErr := json.Marshal(view)
		gomega.Expect(marshalErr != nil).To(gomega.BeFalse(), fmt.Sprintf("error view must be valid JSON: %v", marshalErr))

		var parsed map[string]any
		{

			jsonErr := json.Unmarshal(out, &parsed)
			gomega.Expect(jsonErr != nil).To(gomega.BeFalse(), fmt.Sprintf("error view must round-trip as JSON: %v", jsonErr))
		}

		blockers, ok := parsed["blockers"].([]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "expected blockers to be a JSON array")
		gomega.Expect(blockers).NotTo(gomega.BeEmpty(), "expected at least one blocker in JSON")

		firstBlocker, ok := blockers[0].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "expected blocker to be a map")
		gomega.Expect(firstBlocker["code"] != "invalid_workflow_state").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_workflow_state, got %v", firstBlocker["code"]))

	})
	ginkgo.It("LoadProductStatusValidWorkflowState", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeStatusTestFile(dir, "workflow_state.json", statusTestWorkflowStateJSON())

		view, err := LoadProductStatus(dir, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected no error for valid workflow state, got %v", err))
		gomega.Expect(view.EffortID != "test-effort").To(gomega.BeFalse(), fmt.Sprintf("expected effort_id test-effort, got %q", view.EffortID))
		gomega.Expect(view.TaskID != "impl-test").To(gomega.BeFalse(), fmt.Sprintf("expected task_id impl-test, got %q", view.TaskID))
		gomega.Expect(view.StatusBucket != "started").To(gomega.BeFalse(), fmt.Sprintf("expected status_bucket started, got %q", view.StatusBucket))
		gomega.Expect(view.CurrentPhase != "phase-1").To(gomega.BeFalse(), fmt.Sprintf("expected current_phase phase-1, got %q", view.CurrentPhase))
		gomega.Expect(view.CurrentState != "implementing").To(gomega.BeFalse(), fmt.Sprintf("expected current_state implementing, got %q", view.CurrentState))
		gomega.Expect(view.ContextPackID != "pack").To(gomega.BeFalse(), fmt.Sprintf("expected context_pack_id pack, got %q", view.ContextPackID))
		gomega.Expect(view.FreshnessState != "current").To(gomega.BeFalse(), fmt.Sprintf("expected freshness_state current, got %q", view.FreshnessState))

	})
	ginkgo.It("ProductOperatorStatusViewUsesTypedContinuationActions", func() {
		dir := ginkgo.GinkgoT().TempDir()
		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

		wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["submit_for_verification"]`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{
			Status:       "pass",
			CheckedFiles: []string{"workflow_state.json"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(view.TaskID).To(gomega.Equal("impl-test"), fmt.Sprintf("expected workflow identity fields, got %#v", view))
		gomega.Expect(view.StatusBucket).To(gomega.Equal("started"), fmt.Sprintf("expected workflow identity fields, got %#v", view))
		gomega.Expect(status.WorkflowID).To(gomega.Equal(workflowstate.WorkflowProductDispatch), fmt.Sprintf("status view should project dispatchable product workflow state, got %#v", status))
		gomega.Expect(status.WorkflowState).To(gomega.Equal(workflowstate.StateDispatchable), fmt.Sprintf("status view should project dispatchable product workflow state, got %#v", status))
		gomega.Expect(status.WorkflowTerminalState).To(gomega.BeEmpty(), fmt.Sprintf("status view should project dispatchable product workflow state, got %#v", status))
		gomega.Expect(status.Dispatchable).To(gomega.BeTrue(), fmt.Sprintf("status view should project dispatchable product workflow state, got %#v", status))
		gomega.Expect(view.WorkflowID).To(gomega.Equal(workflowstate.WorkflowProductDispatch), fmt.Sprintf("operator view should project dispatchable product workflow state, got %#v", view))
		gomega.Expect(view.WorkflowState).To(gomega.Equal(workflowstate.StateDispatchable), fmt.Sprintf("operator view should project dispatchable product workflow state, got %#v", view))
		gomega.Expect(view.WorkflowTerminalState).To(gomega.BeEmpty(), fmt.Sprintf("operator view should project dispatchable product workflow state, got %#v", view))
		gomega.Expect(view.Dispatchable).To(gomega.BeTrue(), fmt.Sprintf("operator view should project dispatchable product workflow state, got %#v", view))
		gomega.Expect(len(view.NextValidActions) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected typed continuation actions, got %#v", view.NextValidActions))
		gomega.Expect(len(view.Sections) != 7).To(gomega.BeFalse(), fmt.Sprintf("expected section summary copied into operator view, got %#v", view.Sections))

		action := findOperatorContinuationAction(view.NextValidActions, "submit_for_verification")
		gomega.Expect(action.Label != "submit for verification").To(gomega.BeFalse(), fmt.Sprintf("expected stable label, got %#v", action))
		gomega.Expect(action.Owner).To(gomega.Equal("implementor"), fmt.Sprintf("expected dispatchable typed continuation action, got %#v", action))
		gomega.Expect(action.Dispatchable).To(gomega.BeTrue(), fmt.Sprintf("expected dispatchable typed continuation action, got %#v", action))
		gomega.Expect(action.RunnableNow).To(gomega.BeTrue(), fmt.Sprintf("expected dispatchable typed continuation action, got %#v", action))
		gomega.Expect(len(action.Blockers) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers on dispatchable action, got %#v", action.Blockers))

	})
	ginkgo.It("ProductOperatorStatusViewPreservesClosedTerminalProjection", func() {
		dir := ginkgo.GinkgoT().TempDir()
		wsJSON := statusTestWorkflowStateJSON()
		replacements := map[string]string{
			`"status_bucket":"started"`:         `"status_bucket":"closed"`,
			`"current_phase":"phase-1"`:         `"current_phase":"complete"`,
			`"current_state":"implementing"`:    `"current_state":"closed"`,
			`"open_obligations":["TDD-001"]`:    `"open_obligations":[]`,
			`"next_valid_actions":["complete"]`: `"next_valid_actions":["view_status"]`,
			`"folder_path":"docs/test"`:         `"folder_path":"docs/product-workflows/efforts/closed/impl-test"`,
		}
		for oldText, newText := range replacements {
			wsJSON = strings.Replace(wsJSON, oldText, newText, 1)
		}
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{
			Status:       "pass",
			CheckedFiles: []string{"workflow_state.json"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(status.WorkflowState).To(gomega.Equal(workflowstate.StateTerminal), fmt.Sprintf("status view should project closed terminal state, got %#v", status))
		gomega.Expect(status.WorkflowTerminalState).To(gomega.Equal(workflowstate.TerminalStateComplete), fmt.Sprintf("status view should project closed terminal state, got %#v", status))
		gomega.Expect(status.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("status view should project closed terminal state, got %#v", status))
		gomega.Expect(view.WorkflowState).To(gomega.Equal(workflowstate.StateTerminal), fmt.Sprintf("operator view should preserve closed terminal state, got %#v", view))
		gomega.Expect(view.WorkflowTerminalState).To(gomega.Equal(workflowstate.TerminalStateComplete), fmt.Sprintf("operator view should preserve closed terminal state, got %#v", view))
		gomega.Expect(view.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("operator view should preserve closed terminal state, got %#v", view))

	})
	ginkgo.It("ProductStatusInvalidClosedBucketStateFailsClosed", func() {
		dir := ginkgo.GinkgoT().TempDir()
		wsJSON := statusTestWorkflowStateJSON()
		replacements := map[string]string{
			`"status_bucket":"started"`:         `"status_bucket":"closed"`,
			`"open_obligations":["TDD-001"]`:    `"open_obligations":[]`,
			`"next_valid_actions":["complete"]`: `"next_valid_actions":["inspect_closed_effort"]`,
			`"folder_path":"docs/test"`:         `"folder_path":"docs/product-workflows/efforts/closed/impl-test"`,
		}
		for oldText, newText := range replacements {
			wsJSON = strings.Replace(wsJSON, oldText, newText, 1)
		}
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{
			Status:       "pass",
			CheckedFiles: []string{"workflow_state.json"},
		})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid_state_bucket status error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("invalid_state_bucket"), fmt.Sprintf("expected invalid_state_bucket status error, got %v", err))
		gomega.Expect(status.WorkflowTerminalState).To(gomega.Equal(workflowstate.TerminalStateInvalidScope), fmt.Sprintf("invalid closed bucket/current_state should fail closed before operator view, got %#v", status))
		gomega.Expect(status.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("invalid closed bucket/current_state should fail closed before operator view, got %#v", status))
		gomega.Expect(status.Blockers).NotTo(gomega.BeEmpty(), fmt.Sprintf("status view should expose invalid workflow state blocker, got %#v", status.Blockers))
		gomega.Expect(status.Blockers[0].Code).To(gomega.Equal("invalid_workflow_state"), fmt.Sprintf("status view should expose invalid workflow state blocker, got %#v", status.Blockers))

	})
	ginkgo.It("ProductOperatorStatusViewHumanVerificationApprovalAction", func() {
		dir := ginkgo.GinkgoT().TempDir()
		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"current_state":"implementing"`, `"current_state":"human_verification"`, 1)

		wsJSON = strings.Replace(wsJSON, `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)
		wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["approve_for_implementation"]`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{
			Status:       "pass",
			CheckedFiles: []string{"workflow_state.json"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		action := findOperatorContinuationAction(view.NextValidActions, "approve_for_implementation")
		gomega.Expect(action.Owner).To(gomega.Equal("operator"), fmt.Sprintf("expected runnable operator approval action, got %#v", action))
		gomega.Expect(action.Dispatchable).To(gomega.BeTrue(), fmt.Sprintf("expected runnable operator approval action, got %#v", action))
		gomega.Expect(action.RunnableNow).To(gomega.BeTrue(), fmt.Sprintf("expected runnable operator approval action, got %#v", action))
		gomega.Expect(len(action.Blockers) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers on human approval action, got %#v", action.Blockers))

	})
	ginkgo.It("ProductOperatorStatusViewBlocksStartImplementationShortcut", func() {
		dir := ginkgo.GinkgoT().TempDir()
		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"current_state":"implementing"`, `"current_state":"planned"`, 1)

		wsJSON = strings.Replace(wsJSON, `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)
		wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["start_implementation"]`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		action := findOperatorContinuationAction(view.NextValidActions, "start_implementation")
		gomega.Expect(action.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("expected start_implementation shortcut to be blocked, got %#v", action))
		gomega.Expect(action.RunnableNow).To(gomega.BeFalse(), fmt.Sprintf("expected start_implementation shortcut to be blocked, got %#v", action))
		gomega.Expect(!statusStringsContain(action.BlockedBy, "human_verification_required")).To(gomega.BeFalse(), fmt.Sprintf("expected human_verification_required blocker, got %#v", action))

	})
	ginkgo.It("ProductOperatorStatusViewBlocksStaleDurableActionVocabulary", func() {
		dir := ginkgo.GinkgoT().TempDir()
		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"current_state":"implementing"`, `"current_state":"planned"`, 1)

		wsJSON = strings.Replace(wsJSON, `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)
		wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["approve_prd"]`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(status.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("status view must not be globally dispatchable for stale durable action vocabulary, got %#v", status))
		gomega.Expect(status.WorkflowState).NotTo(gomega.Equal(workflowstate.StateDispatchable), fmt.Sprintf("status view must not be globally dispatchable for stale durable action vocabulary, got %#v", status))
		gomega.Expect(view.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("operator view must not be globally dispatchable for stale durable action vocabulary, got %#v", view))
		gomega.Expect(view.WorkflowState).NotTo(gomega.Equal(workflowstate.StateDispatchable), fmt.Sprintf("operator view must not be globally dispatchable for stale durable action vocabulary, got %#v", view))

		action := findOperatorContinuationAction(view.NextValidActions, "approve_prd")
		gomega.Expect(action.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("expected stale durable action to be blocked, got %#v", action))
		gomega.Expect(action.RunnableNow).To(gomega.BeFalse(), fmt.Sprintf("expected stale durable action to be blocked, got %#v", action))
		gomega.Expect(!statusStringsContain(action.BlockedBy, "invalid_transition_action")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_transition_action blocker, got %#v", action))

	})
	ginkgo.It("ProductOperatorStatusViewIncludesRecoveryPacketState", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeStatusTestFile(dir, "references/recovery_packet.json", statusTestRecoveryPacketJSON())

		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

		wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`, 1)
		wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["submit_for_verification"]`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))
		gomega.Expect(status.Recovery).NotTo(gomega.BeNil(), "expected recovery projection")
		gomega.Expect(status.Recovery.Status).To(gomega.Equal(ProductWorkflowStatusReady), fmt.Sprintf("expected ready executor recovery status, got %#v", status.Recovery))
		gomega.Expect(status.Recovery.FailureClass).To(gomega.Equal(string(recoverycontract.FailureClassExecutorEdit)), fmt.Sprintf("expected ready executor recovery status, got %#v", status.Recovery))
		gomega.Expect(status.Recovery.CandidateAtomCount).To(gomega.Equal(1), fmt.Sprintf("expected recovery candidate gate summary, got %#v", status.Recovery))
		gomega.Expect(statusStringsContain(status.Recovery.Gates, recoverycontract.GateWorkspaceState)).To(gomega.BeTrue(), fmt.Sprintf("expected recovery candidate gate summary, got %#v", status.Recovery))
		gomega.Expect(!statusStringsContain(status.Recovery.RequiredTests, "go test ./internal/workflows/productworkflow")).To(gomega.BeFalse(), fmt.Sprintf("expected recovery required test summary, got %#v", status.Recovery.RequiredTests))
		gomega.Expect(!statusStringsContain(status.Recovery.ProductLinks.PRDPaths, "docs/product-workflows/prd.md")).To(gomega.BeFalse(), fmt.Sprintf("expected bounded product links, got %#v", status.Recovery.ProductLinks))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(view.Recovery).NotTo(gomega.BeNil(), fmt.Sprintf("expected operator recovery projection, got %#v", view.Recovery))
		gomega.Expect(view.Recovery.PacketID).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected operator recovery projection, got %#v", view.Recovery))
		gomega.Expect(!operatorBlockerCodesContain(view.Blockers, "recovery_packet_open")).To(gomega.BeFalse(), fmt.Sprintf("expected open recovery packet to block workflow dispatch, got %#v", view.Blockers))

		workflowAction := findOperatorContinuationAction(view.NextValidActions, "submit_for_verification")
		gomega.Expect(workflowAction.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("expected normal workflow action blocked while recovery packet is open, got %#v", workflowAction))
		gomega.Expect(workflowAction.RunnableNow).To(gomega.BeFalse(), fmt.Sprintf("expected normal workflow action blocked while recovery packet is open, got %#v", workflowAction))

		recoveryAction := findOperatorContinuationAction(view.NextValidActions, "recovery_review_decomposition")
		gomega.Expect(recoveryAction.Owner).To(gomega.Equal("operator"), fmt.Sprintf("expected non-dispatchable runnable recovery action, got %#v", recoveryAction))
		gomega.Expect(recoveryAction.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("expected non-dispatchable runnable recovery action, got %#v", recoveryAction))
		gomega.Expect(recoveryAction.RunnableNow).To(gomega.BeTrue(), fmt.Sprintf("expected non-dispatchable runnable recovery action, got %#v", recoveryAction))
		gomega.Expect(recoveryAction.CommandHint != "idiom recover status --packet references/recovery_packet.json").To(gomega.BeFalse(), fmt.Sprintf("expected recovery status command hint, got %#v", recoveryAction))

	})
	ginkgo.It("LoadProductStatusRedactsUnsafeRecoveryPacketPath", func() {
		dir := ginkgo.GinkgoT().TempDir()
		outsidePath := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")
		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

		wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":`+strconvQuoteStatus(outsidePath)+`}`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))
		gomega.Expect(status.Recovery).NotTo(gomega.BeNil(), "expected recovery projection")
		gomega.Expect(status.Recovery.PacketPath != "outside_effort_root").To(gomega.BeFalse(), fmt.Sprintf("expected unsafe packet path to be redacted, got %#v", status.Recovery))
		gomega.Expect(status.Recovery.Blockers).To(gomega.HaveLen(1), fmt.Sprintf("expected unsafe recovery packet blocker, got %#v", status.Recovery.Blockers))
		gomega.Expect(status.Recovery.Blockers[0].Code).To(gomega.Equal("unsafe_recovery_packet_path"), fmt.Sprintf("expected unsafe recovery packet blocker, got %#v", status.Recovery.Blockers))

		body, err := json.Marshal(status)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal status: %v", err))
		gomega.Expect(strings.Contains(string(body), outsidePath)).To(gomega.BeFalse(), fmt.Sprintf("status leaked unsafe recovery packet path: %s", string(body)))

	})
	ginkgo.It("ProductRecoverySafePathsRejectsBackslashTraversal", func() {
		got := productRecoverySafePaths([]string{
			`docs\product-workflows\prd.md`,
			`..\secret.md`,
			`docs\..\secret.md`,
		})
		gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("expected only normalized safe product link, got %#v", got))
		gomega.Expect(got[0]).To(gomega.Equal("docs/product-workflows/prd.md"), fmt.Sprintf("expected only normalized safe product link, got %#v", got))

	})
	ginkgo.It("ProductOperatorStatusViewUnblocksClosedRecoveryPacket", func() {
		dir := ginkgo.GinkgoT().TempDir()
		packetJSON := statusTestRecoveryPacketJSON()
		writeStatusTestFile(dir, "references/recovery_packet.json", packetJSON)

		var packet recoverycontract.DecompositionPacket
		{

			err := json.Unmarshal([]byte(packetJSON), &packet)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode recovery packet fixture: %v", err))
		}

		reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
			Now:              time.Date(2026, 5, 18, 12, 2, 0, 0, time.UTC),
			CompletedAtomIDs: []string{"recovery-001"},
			Verification:     statusTestRecoveryVerification(packet, "recovery-001"),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildReconciliation: %v", err))

		reconciliationBody, err := json.MarshalIndent(reconciliation, "", "  ")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal reconciliation: %v", err))

		writeStatusTestFile(dir, "references/reconciliation.json", string(reconciliationBody))

		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

		wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`, 1)
		wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["submit_for_verification"]`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))
		gomega.Expect(status.Recovery).NotTo(gomega.BeNil(), fmt.Sprintf("expected closed recovery projection, got %#v", status.Recovery))
		gomega.Expect(status.Recovery.Status).To(gomega.Equal(ProductWorkflowStatusClosed), fmt.Sprintf("expected closed recovery projection, got %#v", status.Recovery))
		gomega.Expect(status.Recovery.CompletedAtomCount).To(gomega.Equal(1), fmt.Sprintf("expected closed recovery projection, got %#v", status.Recovery))
		gomega.Expect(status.Recovery.NextValidAction).To(gomega.Equal(string(recoverycontract.NextActionNone)), fmt.Sprintf("expected closed recovery projection, got %#v", status.Recovery))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(operatorBlockerCodesContain(view.Blockers, "recovery_packet_open")).To(gomega.BeFalse(), fmt.Sprintf("closed recovery packet should not block workflow dispatch, got %#v", view.Blockers))
		gomega.Expect(statusOperatorActionExists(view.NextValidActions, "recovery_review_decomposition")).To(gomega.BeFalse(), fmt.Sprintf("closed recovery packet should not add recovery review action, got %#v", view.NextValidActions))

		workflowAction := findOperatorContinuationAction(view.NextValidActions, "submit_for_verification")
		gomega.Expect(workflowAction.Dispatchable).To(gomega.BeTrue(), fmt.Sprintf("expected workflow action to dispatch after recovery closes, got %#v", workflowAction))
		gomega.Expect(workflowAction.RunnableNow).To(gomega.BeTrue(), fmt.Sprintf("expected workflow action to dispatch after recovery closes, got %#v", workflowAction))

	})
	ginkgo.It("ProductOperatorStatusViewRejectsInvalidRecoveryReconciliation", func() {
		dir := ginkgo.GinkgoT().TempDir()
		packetJSON := statusTestRecoveryPacketJSON()
		writeStatusTestFile(dir, "references/recovery_packet.json", packetJSON)

		var packet recoverycontract.DecompositionPacket
		{

			err := json.Unmarshal([]byte(packetJSON), &packet)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode recovery packet fixture: %v", err))
		}

		reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
			Now:              time.Date(2026, 5, 18, 12, 2, 0, 0, time.UTC),
			CompletedAtomIDs: []string{"recovery-001"},
			Verification:     statusTestRecoveryVerification(packet, "recovery-001"),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildReconciliation: %v", err))

		reconciliation.PacketDigest = ""

		reconciliationBody, err := json.MarshalIndent(reconciliation, "", "  ")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal reconciliation: %v", err))

		writeStatusTestFile(dir, "references/reconciliation.json", string(reconciliationBody))

		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

		wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))
		gomega.Expect(status.Recovery).NotTo(gomega.BeNil(), fmt.Sprintf("invalid reconciliation must not close recovery, got %#v", status.Recovery))
		gomega.Expect(status.Recovery.Status).NotTo(gomega.Equal(ProductWorkflowStatusClosed), fmt.Sprintf("invalid reconciliation must not close recovery, got %#v", status.Recovery))
		gomega.Expect(!productRecoveryBlocksContain(status.Recovery.Blockers, "recovery_reconciliation_missing_packet_digest")).To(gomega.BeFalse(), fmt.Sprintf("expected digest blocker, got %#v", status.Recovery.Blockers))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(!operatorBlockerCodesContain(view.Blockers, "recovery_packet_open")).To(gomega.BeFalse(), fmt.Sprintf("invalid reconciliation should keep workflow blocked, got %#v", view.Blockers))

	})
	ginkgo.It("ProductOperatorStatusViewReportsMalformedDefaultRecoveryReconciliation", func() {
		dir := ginkgo.GinkgoT().TempDir()
		packetJSON := statusTestRecoveryPacketJSON()
		writeStatusTestFile(dir, "references/recovery_packet.json", packetJSON)
		writeStatusTestFile(dir, "references/reconciliation.json", "{not-json")

		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

		wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))
		gomega.Expect(status.Recovery).NotTo(gomega.BeNil(), "expected recovery projection")
		gomega.Expect(!productRecoveryBlocksContain(status.Recovery.Blockers, "invalid_recovery_reconciliation")).To(gomega.BeFalse(), fmt.Sprintf("expected malformed reconciliation blocker, got %#v", status.Recovery.Blockers))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(!operatorBlockerCodesContain(view.Blockers, "recovery_packet_open")).To(gomega.BeFalse(), fmt.Sprintf("malformed reconciliation should keep workflow blocked, got %#v", view.Blockers))

	})
	ginkgo.It("ProductOperatorStatusViewFailsClosedForStaleContextAndBlockers", func() {
		dir := ginkgo.GinkgoT().TempDir()
		wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"freshness_state":"current"`, `"freshness_state":"stale"`, 1)

		wsJSON = strings.Replace(wsJSON, `"open_obligations":["TDD-001"]`, `"open_obligations":["TDD-001"],"blockers":["implementation_pending"]`, 1)
		writeStatusTestFile(dir, "workflow_state.json", wsJSON)

		status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))

		workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(view.SchemaVersion != "idiom.product_operator_status.v1").To(gomega.BeFalse(), fmt.Sprintf("expected operator schema version, got %q", view.SchemaVersion))
		gomega.Expect(view.ContinuationAuthority != "workflow_state").To(gomega.BeFalse(), fmt.Sprintf("expected workflow_state continuation authority, got %q", view.ContinuationAuthority))
		gomega.Expect(view.ArtifactPaths).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected sorted artifact paths from workflow_state, got %#v", view.ArtifactPaths))
		gomega.Expect(view.ArtifactPaths[0].Name).To(gomega.Equal("events"), fmt.Sprintf("expected sorted artifact paths from workflow_state, got %#v", view.ArtifactPaths))
		gomega.Expect(!operatorBlockerCodesContain(view.Blockers, "workflow_blocker")).To(gomega.BeFalse(), fmt.Sprintf("expected workflow blocker, got %#v", view.Blockers))
		gomega.Expect(!operatorBlockerCodesContain(view.Blockers, "stale_context")).To(gomega.BeFalse(), fmt.Sprintf("expected stale context blocker, got %#v", view.Blockers))
		gomega.Expect(!operatorBlockerCodesContain(view.Blockers, "open_obligations")).To(gomega.BeFalse(), fmt.Sprintf("expected open obligations blocker, got %#v", view.Blockers))
		gomega.Expect(view.NextValidActions).NotTo(gomega.BeEmpty(), "expected continuation actions")

		for _, action := range view.NextValidActions {
			gomega.Expect(action.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("expected stale/blocking operator actions to fail closed, got %#v", action))
			gomega.Expect(action.RunnableNow).To(gomega.BeFalse(), fmt.Sprintf("expected blocked workflow continuation to be non-runnable, got %#v", action))
			gomega.Expect(action.Owner == "").To(gomega.BeFalse(), fmt.Sprintf("expected action owner metadata, got %#v", action))
			gomega.Expect(!strings.Contains(strings.Join(action.BlockedBy, ","), "stale_context")).To(gomega.BeFalse(), fmt.Sprintf("expected stale_context dispatch blocker, got %#v", action))

		}
	})
	ginkgo.It("ProductOperatorStatusViewFailsClosedForAuditFailure", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeStatusTestFile(dir, "workflow_state.json", statusTestWorkflowStateJSON())

		status, err := LoadProductStatus(dir, &LintSummaryResult{
			Status:   "fail",
			Findings: []LintSummaryFinding{{Code: "parse_error", Path: "tdd.json", Message: "bad"}},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected status error: %v", err))

		workflowState, err := ParseWorkflowStateJSON([]byte(statusTestWorkflowStateJSON()))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected workflow state parse error: %v", err))

		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(!operatorBlockerCodesContain(view.Blockers, "audit_not_passing")).To(gomega.BeFalse(), fmt.Sprintf("expected audit_not_passing blocker, got %#v", view.Blockers))
		gomega.Expect(view.NextValidActions).NotTo(gomega.BeEmpty(), "expected continuation actions")
		gomega.Expect(view.NextValidActions[0].Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("expected audit failure to block typed continuation, got %#v", view.NextValidActions[0]))
		gomega.Expect(view.NextValidActions[0].RunnableNow).To(gomega.BeFalse(), fmt.Sprintf("expected audit failure to block typed continuation, got %#v", view.NextValidActions[0]))

	})
	ginkgo.It("LoadProductStatusWithLintResultPass", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeStatusTestFile(dir, "workflow_state.json", statusTestWorkflowStateJSON())

		lint := &LintSummaryResult{
			Status:       "pass",
			CheckedFiles: []string{"workflow_state.json", "tdd.json"},
			Findings:     []LintSummaryFinding{},
		}

		view, err := LoadProductStatus(dir, lint)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(view.AuditStatus != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected audit_status pass, got %q", view.AuditStatus))
		gomega.Expect(view.CheckedFileCount != 2).To(gomega.BeFalse(), fmt.Sprintf("expected checked_file_count 2, got %d", view.CheckedFileCount))
		gomega.Expect(len(view.Sections) != 7).To(gomega.BeFalse(), fmt.Sprintf("expected 7 sections, got %d", len(view.Sections)))

		for _, sec := range view.Sections {
			gomega.Expect(sec.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected section %s to pass, got %q", sec.Name, sec.Status))

		}
	})
	ginkgo.It("LoadProductStatusWithLintResultFail", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeStatusTestFile(dir, "workflow_state.json", statusTestWorkflowStateJSON())

		lint := &LintSummaryResult{
			Status:       "fail",
			CheckedFiles: []string{"workflow_state.json", "tdd.json"},
			Findings: []LintSummaryFinding{
				{Code: "parse_error", Path: "tdd.json", Message: "bad"},
				{Code: "parse_error", Path: "prd.md", Message: "bad prd"},
			},
		}

		view, err := LoadProductStatus(dir, lint)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(view.AuditStatus != "fail").To(gomega.BeFalse(), fmt.Sprintf("expected audit_status fail, got %q", view.AuditStatus))
		gomega.Expect(len(view.Findings) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected 2 findings, got %d", len(view.Findings)))

		tddSec := findStatusSection(view.Sections, "tdd")
		gomega.Expect(tddSec.Status != "fail").To(gomega.BeFalse(), fmt.Sprintf("expected tdd section fail, got %q", tddSec.Status))

		prdSec := findStatusSection(view.Sections, "prd")
		gomega.Expect(prdSec.Status != "fail").To(gomega.BeFalse(), fmt.Sprintf("expected prd section fail, got %q", prdSec.Status))

	})
	ginkgo.It("LoadProductStatusWithBlockers", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ws := `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"title":"Test",
		"status_bucket":"started",
		"current_phase":"phase-1",
		"current_state":"blocked",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"stale"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["resolve_blocker"],
		"blockers":["missing_fixture","env_down"]
	}`
		writeStatusTestFile(dir, "workflow_state.json", ws)

		view, err := LoadProductStatus(dir, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(view.Blockers) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected 2 blockers, got %d", len(view.Blockers)))
		gomega.Expect(view.Blockers[0].Code).To(gomega.Equal("workflow_blocker"), fmt.Sprintf("expected workflow_blocker codes, got %q and %q", view.Blockers[0].Code, view.Blockers[1].Code))
		gomega.Expect(view.Blockers[1].Code).To(gomega.Equal("workflow_blocker"), fmt.Sprintf("expected workflow_blocker codes, got %q and %q", view.Blockers[0].Code, view.Blockers[1].Code))

	})
	ginkgo.It("LoadProductStatusOnDurableFixture", func() {
		absRoot := productWorkflowFixturePath()
		ws := loadDurableWorkflowState()
		ctx, _ := ws["context"].(map[string]any)

		view, err := LoadProductStatus(absRoot, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected no error on durable fixture, got %v", err))
		gomega.Expect(view.EffortID != ws["effort_id"]).To(gomega.BeFalse(), fmt.Sprintf("expected effort_id %v, got %q", ws["effort_id"], view.EffortID))
		gomega.Expect(view.TaskID != ws["task_id"]).To(gomega.BeFalse(), fmt.Sprintf("expected task_id %v, got %q", ws["task_id"], view.TaskID))
		gomega.Expect(view.ContextPackID != ctx["context_pack_id"]).To(gomega.BeFalse(), fmt.Sprintf("expected context_pack_id %v, got %q", ctx["context_pack_id"], view.ContextPackID))
		gomega.Expect(view.StateRevision < 1).To(gomega.BeFalse(), fmt.Sprintf("expected positive state_revision, got %d", view.StateRevision))
		gomega.Expect(view.LastEventID != ws["last_event_id"]).To(gomega.BeFalse(), fmt.Sprintf("expected last_event_id %v, got %q", ws["last_event_id"], view.LastEventID))

		wsOO, _ := ws["open_obligations"].([]any)
		gomega.Expect(len(view.OpenObligations) != len(wsOO)).To(gomega.BeFalse(), fmt.Sprintf("expected %d open_obligations (matching workflow_state.json), got %d", len(wsOO), len(view.OpenObligations)))

		wsNVA, _ := ws["next_valid_actions"].([]any)
		gomega.Expect(len(view.NextValidActions) != len(wsNVA)).To(gomega.BeFalse(), fmt.Sprintf("expected %d next_valid_actions (matching workflow_state.json), got %d", len(wsNVA), len(view.NextValidActions)))

		out, marshalErr := json.Marshal(view)
		gomega.Expect(marshalErr != nil).To(gomega.BeFalse(), fmt.Sprintf("status view must be valid JSON: %v", marshalErr))

		var parsed map[string]any
		{

			jsonErr := json.Unmarshal(out, &parsed)
			gomega.Expect(jsonErr != nil).To(gomega.BeFalse(), fmt.Sprintf("status view must round-trip as JSON: %v", jsonErr))
		}

		requiredFields := []string{
			"audit_status", "effort_root", "effort_id", "task_id",
			"status_bucket", "current_phase", "current_state",
			"state_revision", "last_event_id", "context_pack_id",
			"freshness_state", "checked_file_count", "sections",
			"blockers", "open_obligations", "next_valid_actions",
		}
		for _, field := range requiredFields {
			{
				_, ok := parsed[field]
				gomega.Expect(!ok).To(gomega.BeFalse(), fmt.Sprintf("expected field %q in JSON output", field))
			}

		}
	})
})

func containsStatusSubstring(view ProductStatusView, value string) bool {
	for _, blocker := range view.Blockers {
		if strings.Contains(blocker.Message, value) {
			return true
		}
	}

	for _, finding := range view.Findings {
		if strings.Contains(finding.Message, value) {
			return true
		}
	}

	for _, next := range view.NextValidActions {
		if strings.Contains(next, value) {
			return true
		}
	}

	return false
}

func sameStatusTestStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func operatorBlockerCodesContain(blockers []ProductOperatorBlocker, code string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}

	return false
}

func findOperatorContinuationAction(actions []ProductOperatorAction, id string) ProductOperatorAction {
	ginkgo.GinkgoHelper()

	for _, action := range actions {
		if action.ID == id {
			return action
		}
	}
	gomega.Expect(actions).To(gomega.ContainElement(gomega.HaveField("ID", id)), fmt.Sprintf("continuation action %q not found in %#v", id, actions))

	return ProductOperatorAction{}
}

func findStatusSection(sections []StatusSection, name string) StatusSection {
	ginkgo.GinkgoHelper()

	for _, s := range sections {
		if s.Name == name {
			return s
		}
	}
	gomega.Expect(sections).To(gomega.ContainElement(gomega.HaveField("Name", name)), fmt.Sprintf("section %q not found", name))

	return StatusSection{}
}

func loadDurableWorkflowState() map[string]any {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(productWorkflowFixturePath("workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("failed to read workflow_state.json: %v", err))

	var ws map[string]any
	{

		err := json.Unmarshal(data, &ws)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("failed to parse workflow_state.json: %v", err))
	}

	return ws
}

func writeStatusTestFile(dir, relPath, content string) {
	ginkgo.GinkgoHelper()

	fullPath := filepath.Join(dir, relPath)
	{
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", filepath.Dir(fullPath), err))
	}
	{

		err := os.WriteFile(fullPath, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", fullPath, err))
	}

}

func statusTestRecoveryPacketJSON() string {
	ginkgo.GinkgoHelper()

	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "session-product",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		PromptFile:        "docs/product-workflows/prd.md",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/status.go"},
		ChangedPaths:      []string{"internal/workflows/productworkflow/status.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildPlanExecuteFailureRecord: %v", err))

	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{
		Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: recovery.PartialStateForDirtyPaths(
			nil,
			record.PartialState.ChangedPaths,
			record.SourceRun.RequiredEditPaths,
			record.SourceRun.AllowedEditPaths,
			record.PartialState.TotalEdits,
		),
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildDecompositionPacket: %v", err))

	body, err := json.MarshalIndent(packet, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal recovery packet: %v", err))

	return string(body)
}

func statusStringsContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func statusOperatorActionExists(actions []ProductOperatorAction, id string) bool {
	for _, action := range actions {
		if action.ID == id {
			return true
		}
	}

	return false
}

func productRecoveryBlocksContain(blockers []ProductRecoveryBlock, code string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}

	return false
}

func statusTestRecoveryVerification(packet recoverycontract.DecompositionPacket, atomIDs ...string) recoverycontract.ReconciliationVerification {
	completed := map[string]bool{}
	for _, id := range atomIDs {
		completed[id] = true
	}

	seen := map[string]bool{}

	var (
		required []string
		commands []recoverycontract.ReconciliationCommandResult
	)

	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			continue
		}

		for _, test := range atom.RequiredTests {
			if seen[test] {
				continue
			}

			seen[test] = true
			required = append(required, test)
			commands = append(commands, recoverycontract.ReconciliationCommandResult{
				Command:  test,
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			})
		}
	}

	return recoverycontract.ReconciliationVerification{
		Status:        recoverycontract.ReconciliationVerificationPass,
		RequiredTests: required,
		Commands:      commands,
	}
}

func strconvQuoteStatus(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func statusTestWorkflowStateJSON() string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"title":"Test Effort",
		"status_bucket":"started",
		"current_phase":"phase-1",
		"current_state":"implementing",
		"state_revision":5,
		"last_event_id":"evt-0042",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["complete"],
		"open_obligations":["TDD-001"]
	}`
}
