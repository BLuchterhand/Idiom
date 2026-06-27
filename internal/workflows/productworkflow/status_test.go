package productworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"

	"idiom/internal/contracts/workflowstate"
	"idiom/internal/workflows/recovery"
)

func TestLoadProductStatusMissingWorkflowState(t *testing.T) {
	dir := t.TempDir()

	view, err := LoadProductStatus(dir, nil)
	if err == nil {
		t.Fatal("expected error for missing workflow_state.json")
	}

	if view.AuditStatus != "error" {
		t.Fatalf("expected audit_status error, got %q", view.AuditStatus)
	}
	if view.WorkflowID != workflowstate.WorkflowProductDispatch ||
		view.WorkflowState != workflowstate.StateTerminal ||
		view.WorkflowTerminalState != workflowstate.TerminalStateMissingScope ||
		view.Dispatchable {

		t.Fatalf("missing workflow state should project missing-scope terminal state, got %#v", view)
	}

	if len(view.Blockers) == 0 {
		t.Fatal("expected at least one blocker")
	}

	if view.Blockers[0].Code != "missing_workflow_state" {
		t.Fatalf("expected blocker code missing_workflow_state, got %q", view.Blockers[0].Code)
	}

	if view.Blockers[0].Message != "workflow_state.json not found" {
		t.Fatalf("expected privacy-safe missing workflow_state message, got %q", view.Blockers[0].Message)
	}

	if len(view.NextValidActions) != 1 || view.NextValidActions[0] != "restore workflow_state.json under the effort root and rerun product status" {
		t.Fatalf("expected missing workflow_state recovery action, got %#v", view.NextValidActions)
	}

	if containsStatusSubstring(view, dir) {
		t.Fatalf("status error view leaked absolute effort path %q: %#v", dir, view)
	}

	operatorView := BuildProductOperatorStatusView(view, nil)
	if len(operatorView.NextValidActions) == 0 {
		t.Fatalf("expected shared operator recovery view, got %#v", operatorView)
	}

	action := operatorView.NextValidActions[0]
	if action.Owner != "operator" || action.Dispatchable || !action.RunnableNow {
		t.Fatalf("expected non-dispatchable runnable recovery action, got %#v", action)
	}
	if operatorView.WorkflowID != workflowstate.WorkflowProductDispatch ||
		operatorView.WorkflowState != workflowstate.StateTerminal ||
		operatorView.WorkflowTerminalState != workflowstate.TerminalStateMissingScope ||
		operatorView.Dispatchable {

		t.Fatalf("operator error view should preserve product workflow terminal projection, got %#v", operatorView)
	}
}

func TestLoadProductStatusInvalidWorkflowStateJSON(t *testing.T) {
	dir := t.TempDir()
	writeStatusTestFile(t, dir, "workflow_state.json", `{not valid json`)

	view, err := LoadProductStatus(dir, nil)
	if err == nil {
		t.Fatal("expected error for invalid workflow_state.json")
	}

	if view.AuditStatus != "error" {
		t.Fatalf("expected audit_status error, got %q", view.AuditStatus)
	}

	if len(view.Blockers) == 0 {
		t.Fatal("expected at least one blocker")
	}

	if view.Blockers[0].Code != "invalid_workflow_state" {
		t.Fatalf("expected blocker code invalid_workflow_state, got %q", view.Blockers[0].Code)
	}

	if len(view.NextValidActions) != 1 || view.NextValidActions[0] != "fix workflow_state.json content and rerun product status" {
		t.Fatalf("expected invalid workflow_state recovery action, got %#v", view.NextValidActions)
	}

	if containsStatusSubstring(view, dir) {
		t.Fatalf("invalid workflow_state view leaked absolute effort path %q: %#v", dir, view)
	}

	out, marshalErr := json.Marshal(view)
	if marshalErr != nil {
		t.Fatalf("error view must be valid JSON: %v", marshalErr)
	}

	var parsed map[string]any

	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		t.Fatalf("error view must round-trip as JSON: %v", jsonErr)
	}
}

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

func TestLoadProductStatusSemanticInvalidWorkflowState(t *testing.T) {
	dir := t.TempDir()
	writeStatusTestFile(t, dir, "workflow_state.json", `{"schema_version":"idiom.product_workflow_state.v0.fixture"}`)

	view, err := LoadProductStatus(dir, nil)
	if err == nil {
		t.Fatal("expected error for semantically invalid workflow_state.json")
	}

	if view.AuditStatus != "error" {
		t.Fatalf("expected audit_status error, got %q", view.AuditStatus)
	}

	if len(view.Blockers) == 0 {
		t.Fatal("expected at least one blocker")
	}

	if view.Blockers[0].Code != "invalid_workflow_state" {
		t.Fatalf("expected blocker code invalid_workflow_state, got %q", view.Blockers[0].Code)
	}

	out, marshalErr := json.Marshal(view)
	if marshalErr != nil {
		t.Fatalf("error view must be valid JSON: %v", marshalErr)
	}

	var parsed map[string]any

	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		t.Fatalf("error view must round-trip as JSON: %v", jsonErr)
	}

	blockers, ok := parsed["blockers"].([]any)
	if !ok || len(blockers) == 0 {
		t.Fatal("expected at least one blocker in JSON")
	}

	firstBlocker, ok := blockers[0].(map[string]any)
	if !ok {
		t.Fatal("expected blocker to be a map")
	}

	if firstBlocker["code"] != "invalid_workflow_state" {
		t.Fatalf("expected blocker code invalid_workflow_state, got %v", firstBlocker["code"])
	}
}

func TestLoadProductStatusValidWorkflowState(t *testing.T) {
	dir := t.TempDir()
	writeStatusTestFile(t, dir, "workflow_state.json", statusTestWorkflowStateJSON())

	view, err := LoadProductStatus(dir, nil)
	if err != nil {
		t.Fatalf("expected no error for valid workflow state, got %v", err)
	}

	if view.EffortID != "test-effort" {
		t.Fatalf("expected effort_id test-effort, got %q", view.EffortID)
	}

	if view.TaskID != "impl-test" {
		t.Fatalf("expected task_id impl-test, got %q", view.TaskID)
	}

	if view.StatusBucket != "started" {
		t.Fatalf("expected status_bucket started, got %q", view.StatusBucket)
	}

	if view.CurrentPhase != "phase-1" {
		t.Fatalf("expected current_phase phase-1, got %q", view.CurrentPhase)
	}

	if view.CurrentState != "implementing" {
		t.Fatalf("expected current_state implementing, got %q", view.CurrentState)
	}

	if view.ContextPackID != "pack" {
		t.Fatalf("expected context_pack_id pack, got %q", view.ContextPackID)
	}

	if view.FreshnessState != "current" {
		t.Fatalf("expected freshness_state current, got %q", view.FreshnessState)
	}
}

func TestProductOperatorStatusViewUsesTypedContinuationActions(t *testing.T) {
	dir := t.TempDir()
	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

	wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["submit_for_verification"]`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{
		Status:       "pass",
		CheckedFiles: []string{"workflow_state.json"},
	})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if view.TaskID != "impl-test" || view.StatusBucket != "started" {
		t.Fatalf("expected workflow identity fields, got %#v", view)
	}
	if status.WorkflowID != workflowstate.WorkflowProductDispatch ||
		status.WorkflowState != workflowstate.StateDispatchable ||
		status.WorkflowTerminalState != "" ||
		!status.Dispatchable {

		t.Fatalf("status view should project dispatchable product workflow state, got %#v", status)
	}
	if view.WorkflowID != workflowstate.WorkflowProductDispatch ||
		view.WorkflowState != workflowstate.StateDispatchable ||
		view.WorkflowTerminalState != "" ||
		!view.Dispatchable {

		t.Fatalf("operator view should project dispatchable product workflow state, got %#v", view)
	}

	if len(view.NextValidActions) != 1 {
		t.Fatalf("expected typed continuation actions, got %#v", view.NextValidActions)
	}

	if len(view.Sections) != 7 {
		t.Fatalf("expected section summary copied into operator view, got %#v", view.Sections)
	}

	action := findOperatorContinuationAction(t, view.NextValidActions, "submit_for_verification")
	if action.Label != "submit for verification" {
		t.Fatalf("expected stable label, got %#v", action)
	}

	if action.Owner != "implementor" || !action.Dispatchable || !action.RunnableNow {
		t.Fatalf("expected dispatchable typed continuation action, got %#v", action)
	}

	if len(action.Blockers) != 0 {
		t.Fatalf("expected no blockers on dispatchable action, got %#v", action.Blockers)
	}
}

func TestProductOperatorStatusViewPreservesClosedTerminalProjection(t *testing.T) {
	dir := t.TempDir()
	wsJSON := statusTestWorkflowStateJSON()
	replacements := map[string]string{
		`"status_bucket":"started"`:         `"status_bucket":"closed"`,
		`"current_state":"implementing"`:    `"current_state":"closed"`,
		`"open_obligations":["TDD-001"]`:    `"open_obligations":[]`,
		`"next_valid_actions":["complete"]`: `"next_valid_actions":["inspect_closed_effort"]`,
		`"folder_path":"docs/test"`:         `"folder_path":"docs/product-workflows/efforts/closed/impl-test"`,
	}
	for oldText, newText := range replacements {
		wsJSON = strings.Replace(wsJSON, oldText, newText, 1)
	}
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{
		Status:       "pass",
		CheckedFiles: []string{"workflow_state.json"},
	})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if status.WorkflowState != workflowstate.StateTerminal ||
		status.WorkflowTerminalState != workflowstate.TerminalStateComplete ||
		status.Dispatchable {

		t.Fatalf("status view should project closed terminal state, got %#v", status)
	}
	if view.WorkflowState != workflowstate.StateTerminal ||
		view.WorkflowTerminalState != workflowstate.TerminalStateComplete ||
		view.Dispatchable {

		t.Fatalf("operator view should preserve closed terminal state, got %#v", view)
	}
}

func TestProductStatusInvalidClosedBucketStateFailsClosed(t *testing.T) {
	dir := t.TempDir()
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
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{
		Status:       "pass",
		CheckedFiles: []string{"workflow_state.json"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_state_bucket") {
		t.Fatalf("expected invalid_state_bucket status error, got %v", err)
	}

	if status.WorkflowTerminalState != workflowstate.TerminalStateInvalidScope ||
		status.Dispatchable {

		t.Fatalf("invalid closed bucket/current_state should fail closed before operator view, got %#v", status)
	}
	if len(status.Blockers) == 0 || status.Blockers[0].Code != "invalid_workflow_state" {
		t.Fatalf("status view should expose invalid workflow state blocker, got %#v", status.Blockers)
	}
}

func TestProductOperatorStatusViewHumanVerificationApprovalAction(t *testing.T) {
	dir := t.TempDir()
	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"current_state":"implementing"`, `"current_state":"human_verification"`, 1)

	wsJSON = strings.Replace(wsJSON, `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)
	wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["approve_for_implementation"]`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{
		Status:       "pass",
		CheckedFiles: []string{"workflow_state.json"},
	})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	action := findOperatorContinuationAction(t, view.NextValidActions, "approve_for_implementation")

	if action.Owner != "operator" || !action.Dispatchable || !action.RunnableNow {
		t.Fatalf("expected runnable operator approval action, got %#v", action)
	}

	if len(action.Blockers) != 0 {
		t.Fatalf("expected no blockers on human approval action, got %#v", action.Blockers)
	}
}

func TestProductOperatorStatusViewBlocksStartImplementationShortcut(t *testing.T) {
	dir := t.TempDir()
	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"current_state":"implementing"`, `"current_state":"planned"`, 1)

	wsJSON = strings.Replace(wsJSON, `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)
	wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["start_implementation"]`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	action := findOperatorContinuationAction(t, view.NextValidActions, "start_implementation")

	if action.Dispatchable || action.RunnableNow {
		t.Fatalf("expected start_implementation shortcut to be blocked, got %#v", action)
	}

	if !statusStringsContain(action.BlockedBy, "human_verification_required") {
		t.Fatalf("expected human_verification_required blocker, got %#v", action)
	}
}

func TestProductOperatorStatusViewBlocksStaleDurableActionVocabulary(t *testing.T) {
	dir := t.TempDir()
	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"current_state":"implementing"`, `"current_state":"planned"`, 1)

	wsJSON = strings.Replace(wsJSON, `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)
	wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["approve_prd"]`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if status.Dispatchable || status.WorkflowState == workflowstate.StateDispatchable {
		t.Fatalf("status view must not be globally dispatchable for stale durable action vocabulary, got %#v", status)
	}
	if view.Dispatchable || view.WorkflowState == workflowstate.StateDispatchable {
		t.Fatalf("operator view must not be globally dispatchable for stale durable action vocabulary, got %#v", view)
	}

	action := findOperatorContinuationAction(t, view.NextValidActions, "approve_prd")
	if action.Dispatchable || action.RunnableNow {
		t.Fatalf("expected stale durable action to be blocked, got %#v", action)
	}
	if !statusStringsContain(action.BlockedBy, "invalid_transition_action") {
		t.Fatalf("expected invalid_transition_action blocker, got %#v", action)
	}
}

func TestProductOperatorStatusViewIncludesRecoveryPacketState(t *testing.T) {
	dir := t.TempDir()
	writeStatusTestFile(t, dir, "references/recovery_packet.json", statusTestRecoveryPacketJSON(t))

	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

	wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`, 1)
	wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["submit_for_verification"]`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Recovery == nil {
		t.Fatal("expected recovery projection")
	}

	if status.Recovery.Status != "ready" || status.Recovery.FailureClass != string(recoverycontract.FailureClassExecutorEdit) {
		t.Fatalf("expected ready executor recovery status, got %#v", status.Recovery)
	}

	if status.Recovery.CandidateAtomCount != 1 || !statusStringsContain(status.Recovery.Gates, recoverycontract.GateWorkspaceState) {
		t.Fatalf("expected recovery candidate gate summary, got %#v", status.Recovery)
	}

	if !statusStringsContain(status.Recovery.RequiredTests, "go test ./internal/workflows/productworkflow") {
		t.Fatalf("expected recovery required test summary, got %#v", status.Recovery.RequiredTests)
	}

	if !statusStringsContain(status.Recovery.ProductLinks.PRDPaths, "docs/product-workflows/prd.md") {
		t.Fatalf("expected bounded product links, got %#v", status.Recovery.ProductLinks)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if view.Recovery == nil || view.Recovery.PacketID == "" {
		t.Fatalf("expected operator recovery projection, got %#v", view.Recovery)
	}

	if !operatorBlockerCodesContain(view.Blockers, "recovery_packet_open") {
		t.Fatalf("expected open recovery packet to block workflow dispatch, got %#v", view.Blockers)
	}

	workflowAction := findOperatorContinuationAction(t, view.NextValidActions, "submit_for_verification")
	if workflowAction.Dispatchable || workflowAction.RunnableNow {
		t.Fatalf("expected normal workflow action blocked while recovery packet is open, got %#v", workflowAction)
	}

	recoveryAction := findOperatorContinuationAction(t, view.NextValidActions, "recovery_review_decomposition")
	if recoveryAction.Owner != "operator" || recoveryAction.Dispatchable || !recoveryAction.RunnableNow {
		t.Fatalf("expected non-dispatchable runnable recovery action, got %#v", recoveryAction)
	}

	if recoveryAction.CommandHint != "idiom recover status --packet references/recovery_packet.json" {
		t.Fatalf("expected recovery status command hint, got %#v", recoveryAction)
	}
}

func TestLoadProductStatusRedactsUnsafeRecoveryPacketPath(t *testing.T) {
	dir := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "packet.json")
	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

	wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":`+strconvQuoteStatus(outsidePath)+`}`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Recovery == nil {
		t.Fatal("expected recovery projection")
	}

	if status.Recovery.PacketPath != "outside_effort_root" {
		t.Fatalf("expected unsafe packet path to be redacted, got %#v", status.Recovery)
	}

	if len(status.Recovery.Blockers) != 1 || status.Recovery.Blockers[0].Code != "unsafe_recovery_packet_path" {
		t.Fatalf("expected unsafe recovery packet blocker, got %#v", status.Recovery.Blockers)
	}

	body, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}

	if strings.Contains(string(body), outsidePath) {
		t.Fatalf("status leaked unsafe recovery packet path: %s", string(body))
	}
}

func TestProductRecoverySafePathsRejectsBackslashTraversal(t *testing.T) {
	got := productRecoverySafePaths([]string{
		`docs\product-workflows\prd.md`,
		`..\secret.md`,
		`docs\..\secret.md`,
	})
	if len(got) != 1 || got[0] != "docs/product-workflows/prd.md" {
		t.Fatalf("expected only normalized safe product link, got %#v", got)
	}
}

func TestProductOperatorStatusViewUnblocksClosedRecoveryPacket(t *testing.T) {
	dir := t.TempDir()
	packetJSON := statusTestRecoveryPacketJSON(t)
	writeStatusTestFile(t, dir, "references/recovery_packet.json", packetJSON)

	var packet recoverycontract.DecompositionPacket

	if err := json.Unmarshal([]byte(packetJSON), &packet); err != nil {
		t.Fatalf("decode recovery packet fixture: %v", err)
	}

	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 12, 2, 0, 0, time.UTC),
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     statusTestRecoveryVerification(packet, "recovery-001"),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	reconciliationBody, err := json.MarshalIndent(reconciliation, "", "  ")
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}

	writeStatusTestFile(t, dir, "references/reconciliation.json", string(reconciliationBody))

	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

	wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`, 1)
	wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["complete"]`, `"next_valid_actions":["submit_for_verification"]`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Recovery == nil || status.Recovery.Status != "closed" || status.Recovery.CompletedAtomCount != 1 || status.Recovery.NextValidAction != string(recoverycontract.NextActionNone) {
		t.Fatalf("expected closed recovery projection, got %#v", status.Recovery)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if operatorBlockerCodesContain(view.Blockers, "recovery_packet_open") {
		t.Fatalf("closed recovery packet should not block workflow dispatch, got %#v", view.Blockers)
	}

	if statusOperatorActionExists(view.NextValidActions, "recovery_review_decomposition") {
		t.Fatalf("closed recovery packet should not add recovery review action, got %#v", view.NextValidActions)
	}

	workflowAction := findOperatorContinuationAction(t, view.NextValidActions, "submit_for_verification")
	if !workflowAction.Dispatchable || !workflowAction.RunnableNow {
		t.Fatalf("expected workflow action to dispatch after recovery closes, got %#v", workflowAction)
	}
}

func TestProductOperatorStatusViewRejectsInvalidRecoveryReconciliation(t *testing.T) {
	dir := t.TempDir()
	packetJSON := statusTestRecoveryPacketJSON(t)
	writeStatusTestFile(t, dir, "references/recovery_packet.json", packetJSON)

	var packet recoverycontract.DecompositionPacket

	if err := json.Unmarshal([]byte(packetJSON), &packet); err != nil {
		t.Fatalf("decode recovery packet fixture: %v", err)
	}

	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 12, 2, 0, 0, time.UTC),
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     statusTestRecoveryVerification(packet, "recovery-001"),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	reconciliation.PacketDigest = ""

	reconciliationBody, err := json.MarshalIndent(reconciliation, "", "  ")
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}

	writeStatusTestFile(t, dir, "references/reconciliation.json", string(reconciliationBody))

	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

	wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Recovery == nil || status.Recovery.Status == "closed" {
		t.Fatalf("invalid reconciliation must not close recovery, got %#v", status.Recovery)
	}

	if !productRecoveryBlocksContain(status.Recovery.Blockers, "recovery_reconciliation_missing_packet_digest") {
		t.Fatalf("expected digest blocker, got %#v", status.Recovery.Blockers)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if !operatorBlockerCodesContain(view.Blockers, "recovery_packet_open") {
		t.Fatalf("invalid reconciliation should keep workflow blocked, got %#v", view.Blockers)
	}
}

func TestProductOperatorStatusViewReportsMalformedDefaultRecoveryReconciliation(t *testing.T) {
	dir := t.TempDir()
	packetJSON := statusTestRecoveryPacketJSON(t)
	writeStatusTestFile(t, dir, "references/recovery_packet.json", packetJSON)
	writeStatusTestFile(t, dir, "references/reconciliation.json", "{not-json")

	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"open_obligations":["TDD-001"]`, `"open_obligations":[]`, 1)

	wsJSON = strings.Replace(wsJSON, `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Recovery == nil {
		t.Fatal("expected recovery projection")
	}

	if !productRecoveryBlocksContain(status.Recovery.Blockers, "invalid_recovery_reconciliation") {
		t.Fatalf("expected malformed reconciliation blocker, got %#v", status.Recovery.Blockers)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if !operatorBlockerCodesContain(view.Blockers, "recovery_packet_open") {
		t.Fatalf("malformed reconciliation should keep workflow blocked, got %#v", view.Blockers)
	}
}

func TestProductOperatorStatusViewFailsClosedForStaleContextAndBlockers(t *testing.T) {
	dir := t.TempDir()
	wsJSON := strings.Replace(statusTestWorkflowStateJSON(), `"freshness_state":"current"`, `"freshness_state":"stale"`, 1)

	wsJSON = strings.Replace(wsJSON, `"open_obligations":["TDD-001"]`, `"open_obligations":["TDD-001"],"blockers":["implementation_pending"]`, 1)
	writeStatusTestFile(t, dir, "workflow_state.json", wsJSON)

	status, err := LoadProductStatus(dir, &LintSummaryResult{Status: "pass"})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(wsJSON))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if view.SchemaVersion != "idiom.product_operator_status.v1" {
		t.Fatalf("expected operator schema version, got %q", view.SchemaVersion)
	}

	if view.ContinuationAuthority != "workflow_state" {
		t.Fatalf("expected workflow_state continuation authority, got %q", view.ContinuationAuthority)
	}

	if len(view.ArtifactPaths) == 0 || view.ArtifactPaths[0].Name != "events" {
		t.Fatalf("expected sorted artifact paths from workflow_state, got %#v", view.ArtifactPaths)
	}

	if !operatorBlockerCodesContain(view.Blockers, "workflow_blocker") {
		t.Fatalf("expected workflow blocker, got %#v", view.Blockers)
	}

	if !operatorBlockerCodesContain(view.Blockers, "stale_context") {
		t.Fatalf("expected stale context blocker, got %#v", view.Blockers)
	}

	if !operatorBlockerCodesContain(view.Blockers, "open_obligations") {
		t.Fatalf("expected open obligations blocker, got %#v", view.Blockers)
	}

	if len(view.NextValidActions) == 0 {
		t.Fatal("expected continuation actions")
	}

	for _, action := range view.NextValidActions {
		if action.Dispatchable {
			t.Fatalf("expected stale/blocking operator actions to fail closed, got %#v", action)
		}

		if action.RunnableNow {
			t.Fatalf("expected blocked workflow continuation to be non-runnable, got %#v", action)
		}

		if action.Owner == "" {
			t.Fatalf("expected action owner metadata, got %#v", action)
		}

		if !strings.Contains(strings.Join(action.BlockedBy, ","), "stale_context") {
			t.Fatalf("expected stale_context dispatch blocker, got %#v", action)
		}
	}
}

func TestProductOperatorStatusViewFailsClosedForAuditFailure(t *testing.T) {
	dir := t.TempDir()
	writeStatusTestFile(t, dir, "workflow_state.json", statusTestWorkflowStateJSON())

	status, err := LoadProductStatus(dir, &LintSummaryResult{
		Status:   "fail",
		Findings: []LintSummaryFinding{{Code: "parse_error", Path: "tdd.json", Message: "bad"}},
	})
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	workflowState, err := ParseWorkflowStateJSON([]byte(statusTestWorkflowStateJSON()))
	if err != nil {
		t.Fatalf("unexpected workflow state parse error: %v", err)
	}

	view := BuildProductOperatorStatusView(status, &workflowState)
	if !operatorBlockerCodesContain(view.Blockers, "audit_not_passing") {
		t.Fatalf("expected audit_not_passing blocker, got %#v", view.Blockers)
	}

	if len(view.NextValidActions) == 0 {
		t.Fatal("expected continuation actions")
	}

	if view.NextValidActions[0].Dispatchable || view.NextValidActions[0].RunnableNow {
		t.Fatalf("expected audit failure to block typed continuation, got %#v", view.NextValidActions[0])
	}
}

func TestLoadProductStatusWithLintResultPass(t *testing.T) {
	dir := t.TempDir()
	writeStatusTestFile(t, dir, "workflow_state.json", statusTestWorkflowStateJSON())

	lint := &LintSummaryResult{
		Status:       "pass",
		CheckedFiles: []string{"workflow_state.json", "tdd.json"},
		Findings:     []LintSummaryFinding{},
	}

	view, err := LoadProductStatus(dir, lint)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if view.AuditStatus != "pass" {
		t.Fatalf("expected audit_status pass, got %q", view.AuditStatus)
	}

	if view.CheckedFileCount != 2 {
		t.Fatalf("expected checked_file_count 2, got %d", view.CheckedFileCount)
	}

	if len(view.Sections) != 7 {
		t.Fatalf("expected 7 sections, got %d", len(view.Sections))
	}

	for _, sec := range view.Sections {
		if sec.Status != "pass" {
			t.Fatalf("expected section %s to pass, got %q", sec.Name, sec.Status)
		}
	}
}

func TestLoadProductStatusWithLintResultFail(t *testing.T) {
	dir := t.TempDir()
	writeStatusTestFile(t, dir, "workflow_state.json", statusTestWorkflowStateJSON())

	lint := &LintSummaryResult{
		Status:       "fail",
		CheckedFiles: []string{"workflow_state.json", "tdd.json"},
		Findings: []LintSummaryFinding{
			{Code: "parse_error", Path: "tdd.json", Message: "bad"},
			{Code: "parse_error", Path: "prd.md", Message: "bad prd"},
		},
	}

	view, err := LoadProductStatus(dir, lint)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if view.AuditStatus != "fail" {
		t.Fatalf("expected audit_status fail, got %q", view.AuditStatus)
	}

	if len(view.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(view.Findings))
	}

	tddSec := findStatusSection(t, view.Sections, "tdd")
	if tddSec.Status != "fail" {
		t.Fatalf("expected tdd section fail, got %q", tddSec.Status)
	}

	prdSec := findStatusSection(t, view.Sections, "prd")
	if prdSec.Status != "fail" {
		t.Fatalf("expected prd section fail, got %q", prdSec.Status)
	}
}

func TestLoadProductStatusWithBlockers(t *testing.T) {
	dir := t.TempDir()
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
	writeStatusTestFile(t, dir, "workflow_state.json", ws)

	view, err := LoadProductStatus(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(view.Blockers) != 2 {
		t.Fatalf("expected 2 blockers, got %d", len(view.Blockers))
	}

	if view.Blockers[0].Code != "workflow_blocker" || view.Blockers[1].Code != "workflow_blocker" {
		t.Fatalf("expected workflow_blocker codes, got %q and %q", view.Blockers[0].Code, view.Blockers[1].Code)
	}
}

func TestLoadProductStatusOnDurableFixture(t *testing.T) {
	absRoot := productWorkflowFixturePath(t)
	ws := loadDurableWorkflowState(t)
	ctx, _ := ws["context"].(map[string]any)

	view, err := LoadProductStatus(absRoot, nil)
	if err != nil {
		t.Fatalf("expected no error on durable fixture, got %v", err)
	}

	if view.EffortID != ws["effort_id"] {
		t.Fatalf("expected effort_id %v, got %q", ws["effort_id"], view.EffortID)
	}

	if view.TaskID != ws["task_id"] {
		t.Fatalf("expected task_id %v, got %q", ws["task_id"], view.TaskID)
	}

	if view.ContextPackID != ctx["context_pack_id"] {
		t.Fatalf("expected context_pack_id %v, got %q", ctx["context_pack_id"], view.ContextPackID)
	}

	if view.StateRevision < 1 {
		t.Fatalf("expected positive state_revision, got %d", view.StateRevision)
	}

	if view.LastEventID != ws["last_event_id"] {
		t.Fatalf("expected last_event_id %v, got %q", ws["last_event_id"], view.LastEventID)
	}

	wsOO, _ := ws["open_obligations"].([]any)
	if len(view.OpenObligations) != len(wsOO) {
		t.Fatalf("expected %d open_obligations (matching workflow_state.json), got %d", len(wsOO), len(view.OpenObligations))
	}

	wsNVA, _ := ws["next_valid_actions"].([]any)
	if len(view.NextValidActions) != len(wsNVA) {
		t.Fatalf("expected %d next_valid_actions (matching workflow_state.json), got %d", len(wsNVA), len(view.NextValidActions))
	}

	out, marshalErr := json.Marshal(view)
	if marshalErr != nil {
		t.Fatalf("status view must be valid JSON: %v", marshalErr)
	}

	var parsed map[string]any

	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		t.Fatalf("status view must round-trip as JSON: %v", jsonErr)
	}

	requiredFields := []string{
		"audit_status", "effort_root", "effort_id", "task_id",
		"status_bucket", "current_phase", "current_state",
		"state_revision", "last_event_id", "context_pack_id",
		"freshness_state", "checked_file_count", "sections",
		"blockers", "open_obligations", "next_valid_actions",
	}
	for _, field := range requiredFields {
		if _, ok := parsed[field]; !ok {
			t.Fatalf("expected field %q in JSON output", field)
		}
	}
}

func operatorBlockerCodesContain(blockers []ProductOperatorBlocker, code string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}

	return false
}

func findOperatorContinuationAction(t *testing.T, actions []ProductOperatorAction, id string) ProductOperatorAction {
	t.Helper()

	for _, action := range actions {
		if action.ID == id {
			return action
		}
	}

	t.Fatalf("continuation action %q not found in %#v", id, actions)

	return ProductOperatorAction{}
}

func findStatusSection(t *testing.T, sections []StatusSection, name string) StatusSection {
	t.Helper()

	for _, s := range sections {
		if s.Name == name {
			return s
		}
	}

	t.Fatalf("section %q not found", name)

	return StatusSection{}
}

func statusRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("could not find repo root")
		}

		dir = next
	}
}

func loadDurableWorkflowState(t *testing.T) map[string]any {
	t.Helper()

	data, err := os.ReadFile(productWorkflowFixturePath(t, "workflow_state.json"))
	if err != nil {
		t.Fatalf("failed to read workflow_state.json: %v", err)
	}

	var ws map[string]any

	if err := json.Unmarshal(data, &ws); err != nil {
		t.Fatalf("failed to parse workflow_state.json: %v", err)
	}

	return ws
}

func writeStatusTestFile(t *testing.T, dir, relPath, content string) {
	t.Helper()

	fullPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", fullPath, err)
	}
}

func statusTestRecoveryPacketJSON(t *testing.T) string {
	t.Helper()

	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "session-product",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		PromptFile:        "docs/product-workflows/prd.md",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/status.go"},
		ChangedPaths:      []string{"internal/workflows/productworkflow/status.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}

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
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}

	body, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		t.Fatalf("marshal recovery packet: %v", err)
	}

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
