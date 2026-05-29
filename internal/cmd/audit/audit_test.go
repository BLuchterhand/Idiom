package audit
import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditWatchOnceWritesReviewForFeedbackTelemetry(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	telemetryDir := filepath.Join(root, "logs", "telemetry")
	if err := os.MkdirAll(telemetryDir, 0o755); err != nil {
		t.Fatalf("mkdir telemetry: %v", err)
	}
	writeAuditWatchFile(t, filepath.Join(telemetryDir, "feedback.jsonl"), strings.Join([]string{
		`{"type":"session_start","command":"tool:harness_feedback","workspace_root":"` + root + `"}`,
		`{"type":"tool_result","tool":"harness_feedback","summary":"Recorded workflow_gap feedback for completion_gate","error":"","feedback_category":"workflow_gap","feedback_component":"completion_gate","feedback_confidence":"verified","feedback_severity":"medium"}`,
		`{"type":"process_exit","command":"tool:harness_feedback","exit_code":0}`,
		"",
	}, "\n"))
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{
		"watch",
		"--config", configPath,
		"--paths", "logs/telemetry",
		"--output", outputDir,
		"--once",
		"--review-existing",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "audit review:") {
		t.Fatalf("expected review path on stdout, got %q", stdout.String())
	}
	reviews := auditWatchReviewFiles(t, outputDir)
	if len(reviews) != 1 {
		t.Fatalf("expected one review artifact, got %#v", reviews)
	}
	var review auditWatchReview
	data, err := os.ReadFile(reviews[0])
	if err != nil {
		t.Fatalf("read review: %v", err)
	}
	if err := json.Unmarshal(data, &review); err != nil {
		t.Fatalf("decode review: %v\n%s", err, string(data))
	}
	if strings.Contains(string(data), root) {
		t.Fatalf("review packet leaked raw workspace root: %s", string(data))
	}
	if review.SchemaVersion != auditWatchPacketSchemaVersion {
		t.Fatalf("expected audit-watch packet schema, got %q", review.SchemaVersion)
	}
	if review.Status != "open" || review.PromotionStatus != "open" {
		t.Fatalf("expected open workflow packet, status=%q promotion=%q", review.Status, review.PromotionStatus)
	}
	if review.OwnerSurface == "" || review.TopFailureClass == "" {
		t.Fatalf("expected owner surface and failure class, got owner=%q top=%q", review.OwnerSurface, review.TopFailureClass)
	}
	if len(review.TriggerFiles) != 1 || !slicesContains(review.TriggerFiles[0].Reasons, "harness_feedback") {
		t.Fatalf("expected harness_feedback trigger, got %#v", review.TriggerFiles)
	}
	if len(review.Audits) != 1 || review.Audits[0].Report == nil {
		t.Fatalf("expected embedded harness audit report, got %#v", review.Audits)
	}
	if len(review.Findings) == 0 {
		t.Fatalf("expected owned findings from trigger/audit evidence, got %#v", review)
	}
	for _, finding := range review.Findings {
		if finding.Severity != "high" && finding.Severity != "medium" {
			continue
		}
		if finding.OwnerSurface == "" || finding.Status != "open" || finding.Reproducer.AuditCommand == "" {
			t.Fatalf("finding missing owner/status/reproducer: %#v", finding)
		}
		if len(finding.Promotion.CandidateTargets) == 0 || finding.Promotion.RequiredDecision == "" {
			t.Fatalf("finding missing regression-promotion path: %#v", finding)
		}
		if finding.Closure.Ready || finding.Closure.Reason != "before_after_replay_required" {
			t.Fatalf("open finding should require replay closure evidence: %#v", finding.Closure)
		}
	}
	if !slicesContains(review.CandidateRegressions, "regression-trigger-harness-feedback") {
		t.Fatalf("expected trigger candidate regression, got %#v", review.CandidateRegressions)
	}
	if len(review.ClientClassifications) != 7 {
		t.Fatalf("expected all client classifications, got %#v", review.ClientClassifications)
	}
	for _, want := range []string{"finance", "healthcare_life_sciences", "public_sector", "enterprise_saas", "tooling_vendor", "local_model_private_code", "open_source"} {
		if !auditWatchReviewHasClientClassification(review, want) {
			t.Fatalf("missing client classification %q in %#v", want, review.ClientClassifications)
		}
	}
	for _, want := range []string{"security_privacy", "retained_use", "integration_proof", "client_demo_proof", "packaging_support", "local_product_evidence_export"} {
		if !auditWatchReviewHasProductGateImpact(review, want) {
			t.Fatalf("missing product-success gate impact %q in %#v", want, review.ProductSuccessGateImpacts)
		}
	}
	if review.Privacy.EvidencePolicy != "bounded_audit_metadata_only" || review.Privacy.PromptTextIncluded || review.Privacy.FileBodiesIncluded || review.Privacy.LogExcerptsIncluded || review.Privacy.SecretsIncluded {
		t.Fatalf("expected bounded privacy policy, got %#v", review.Privacy)
	}
}

func TestAuditWatchSanitizesSensitiveTriggerSessionAndReproducerEvidence(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	telemetryDir := filepath.Join(root, "logs", "telemetry", "token=SECRET")
	if err := os.MkdirAll(telemetryDir, 0o755); err != nil {
		t.Fatalf("mkdir telemetry: %v", err)
	}
	writeAuditWatchFile(t, filepath.Join(telemetryDir, "feedback.jsonl"), strings.Join([]string{
		`{"type":"tool_result","tool":"harness_feedback","session_id":"token=SESSIONSECRET","summary":"Recorded workflow_gap feedback","error":""}`,
		`{"type":"completion_status","status":"blocked","reason":"token=BLOCKSECRET","session_id":"token=SESSIONSECRET"}`,
		"",
	}, "\n"))
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{
		"watch",
		"--config", configPath,
		"--paths", "logs/telemetry/token=SECRET",
		"--output", outputDir,
		"--once",
		"--review-existing",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	reviews := auditWatchReviewFiles(t, outputDir)
	if len(reviews) != 1 {
		t.Fatalf("expected one review artifact, got %#v stdout=%s", reviews, stdout.String())
	}
	data, err := os.ReadFile(reviews[0])
	if err != nil {
		t.Fatalf("read review: %v", err)
	}
	packet := string(data)
	for _, forbidden := range []string{root, "token=SECRET", "token=SESSIONSECRET", "token=BLOCKSECRET", "SESSIONSECRET", "BLOCKSECRET"} {
		if strings.Contains(packet, forbidden) {
			t.Fatalf("audit-watch packet leaked %q: %s", forbidden, packet)
		}
	}

	var review auditWatchReview
	if err := json.Unmarshal(data, &review); err != nil {
		t.Fatalf("decode review: %v\n%s", err, packet)
	}
	if review.SchemaVersion != auditWatchPacketSchemaVersion || review.Status != "open" || review.PromotionStatus != "open" {
		t.Fatalf("expected open schema-versioned packet, got schema=%q status=%q promotion=%q", review.SchemaVersion, review.Status, review.PromotionStatus)
	}
	if len(review.TriggerFiles) != 1 || review.TriggerFiles[0].Path == "" || !strings.Contains(review.TriggerFiles[0].Path, "redacted") {
		t.Fatalf("expected redacted trigger path, got %#v", review.TriggerFiles)
	}
	if len(review.TriggerFiles[0].Sessions) != 1 || !strings.Contains(review.TriggerFiles[0].Sessions[0], "redacted") {
		t.Fatalf("expected redacted trigger session, got %#v", review.TriggerFiles[0].Sessions)
	}
	if !slicesContains(review.TriggerFiles[0].Reasons, "completion_status_blocked:redacted") {
		t.Fatalf("expected sensitive completion reason to be bounded, got %#v", review.TriggerFiles[0].Reasons)
	}
	if len(review.Reproduce.Evidence) == 0 || !strings.Contains(strings.Join(review.Reproduce.Evidence, " "), "redacted") {
		t.Fatalf("expected redacted packet reproducer evidence, got %#v", review.Reproduce)
	}
	if len(review.Findings) == 0 {
		t.Fatalf("expected findings to remain present: %#v", review)
	}
	for _, finding := range review.Findings {
		if finding.OwnerSurface == "" || finding.Status == "" || finding.Reproducer.AuditCommand == "" || len(finding.Promotion.CandidateTargets) == 0 || finding.Closure.Reason == "" {
			t.Fatalf("finding lost workflow fields after sanitization: %#v", finding)
		}
	}
	if len(review.ClientClassifications) != 7 {
		t.Fatalf("expected all client classifications, got %#v", review.ClientClassifications)
	}
	for _, want := range []string{"security_privacy", "retained_use", "integration_proof", "client_demo_proof", "packaging_support", "local_product_evidence_export"} {
		if !auditWatchReviewHasProductGateImpact(review, want) {
			t.Fatalf("missing product-success gate impact %q in %#v", want, review.ProductSuccessGateImpacts)
		}
	}
}

func TestAuditWatchSanitizesCustomerAndRawTranscriptEvidence(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	telemetryDir := filepath.Join(root, "logs", "telemetry", "secrets", "customer-acme")
	if err := os.MkdirAll(telemetryDir, 0o755); err != nil {
		t.Fatalf("mkdir telemetry: %v", err)
	}
	writeAuditWatchFile(t, filepath.Join(telemetryDir, "feedback.jsonl"), strings.Join([]string{
		`{"type":"tool_result","tool":"harness_feedback","session_id":"customer-acme-session","summary":"Recorded workflow_gap feedback","error":""}`,
		`{"type":"completion_status","status":"blocked","reason":"raw_prompt patient transcript body","session_id":"raw_prompt customer transcript"}`,
		"",
	}, "\n"))
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{
		"watch",
		"--config", configPath,
		"--paths", "logs/telemetry/secrets/customer-acme",
		"--output", outputDir,
		"--once",
		"--review-existing",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	reviews := auditWatchReviewFiles(t, outputDir)
	if len(reviews) != 1 {
		t.Fatalf("expected one review artifact, got %#v stdout=%s", reviews, stdout.String())
	}
	data, err := os.ReadFile(reviews[0])
	if err != nil {
		t.Fatalf("read review: %v", err)
	}
	packet := string(data)
	for _, forbidden := range []string{
		root,
		"logs/telemetry/secrets/customer-acme/feedback.jsonl",
		"logs/telemetry/secrets/customer-acme",
		"secrets/customer-acme",
		"customer-acme-session",
		"raw_prompt customer transcript",
		"raw_prompt patient transcript body",
		"patient_transcript_body",
		"customer transcript",
	} {
		if strings.Contains(packet, forbidden) {
			t.Fatalf("audit-watch packet leaked %q: %s", forbidden, packet)
		}
	}

	var review auditWatchReview
	if err := json.Unmarshal(data, &review); err != nil {
		t.Fatalf("decode review: %v\n%s", err, packet)
	}
	if len(review.TriggerFiles) != 1 {
		t.Fatalf("expected one trigger file, got %#v", review.TriggerFiles)
	}
	trigger := review.TriggerFiles[0]
	if trigger.Path == "" || !strings.Contains(trigger.Path, "redacted_path") {
		t.Fatalf("expected sensitive trigger path to be redacted, got %#v", trigger)
	}
	if len(trigger.Sessions) != 2 {
		t.Fatalf("expected both sessions to remain as bounded evidence, got %#v", trigger.Sessions)
	}
	joinedSessions := strings.Join(trigger.Sessions, " ")
	if !strings.Contains(joinedSessions, "redacted_customer") || !strings.Contains(joinedSessions, "redacted_raw-content") {
		t.Fatalf("expected customer and raw-content sessions to be bounded, got %#v", trigger.Sessions)
	}
	if !slicesContains(trigger.Reasons, "harness_feedback") || !slicesContains(trigger.Reasons, "completion_status_blocked:redacted") {
		t.Fatalf("expected useful bounded reasons to remain, got %#v", trigger.Reasons)
	}
	if len(review.Reproduce.Evidence) == 0 || !strings.Contains(strings.Join(review.Reproduce.Evidence, " "), "redacted_path") {
		t.Fatalf("expected redacted packet reproducer evidence, got %#v", review.Reproduce)
	}
	if len(review.Findings) == 0 || len(review.ClientClassifications) != 7 {
		t.Fatalf("expected findings and client classifications to remain present: findings=%#v clients=%#v", review.Findings, review.ClientClassifications)
	}
}

func TestAuditWatchFirstRunBaselinesWithoutReviewExisting(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	telemetryDir := filepath.Join(root, "logs", "telemetry")
	if err := os.MkdirAll(telemetryDir, 0o755); err != nil {
		t.Fatalf("mkdir telemetry: %v", err)
	}
	writeAuditWatchFile(t, filepath.Join(telemetryDir, "feedback.jsonl"), `{"type":"tool_result","tool":"harness_feedback","summary":"Recorded feedback","error":""}`+"\n")
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{
		"watch",
		"--config", configPath,
		"--paths", "logs/telemetry",
		"--output", outputDir,
		"--once",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "no triggers") {
		t.Fatalf("expected baseline no-trigger status, got %q", stdout.String())
	}
	if reviews := auditWatchReviewFiles(t, outputDir); len(reviews) != 0 {
		t.Fatalf("expected no review artifacts, got %#v", reviews)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "state.json")); err != nil {
		t.Fatalf("expected state file: %v", err)
	}
}

func TestAuditWatchDefaultPathsIncludeConversationLogs(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	writeAuditWatchFile(t, filepath.Join(root, "logs", "conversations", "completion.jsonl"), strings.Join([]string{
		`{"type":"tool_result","tool":"completion_gate","summary":"Mutation completion gate rejected final answer","error":"no successful write tool result is present"}`,
		"",
	}, "\n"))
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{
		"watch",
		"--config", configPath,
		"--output", outputDir,
		"--once",
		"--review-existing",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	reviews := auditWatchReviewFiles(t, outputDir)
	if len(reviews) != 1 {
		t.Fatalf("expected one review artifact from default logs/conversations path, got %#v stdout=%s", reviews, stdout.String())
	}
	var review auditWatchReview
	data, err := os.ReadFile(reviews[0])
	if err != nil {
		t.Fatalf("read review: %v", err)
	}
	if err := json.Unmarshal(data, &review); err != nil {
		t.Fatalf("decode review: %v\n%s", err, string(data))
	}
	if len(review.TriggerFiles) != 1 || !strings.Contains(review.TriggerFiles[0].Path, "logs/conversations") {
		t.Fatalf("expected trigger from logs/conversations, got %#v", review.TriggerFiles)
	}
	if len(review.Audits) == 0 {
		t.Fatalf("expected embedded harness audits for default paths")
	}
}

func TestAuditWatchTriggersOnBlockedCompletionStatus(t *testing.T) {
	reasons := auditWatchTriggerReasons(map[string]any{
		"type":         "completion_status",
		"status":       "blocked",
		"reason":       "required_edit_paths_remaining",
		"blocker_kind": "required_edit_paths_remaining",
	})
	if !slicesContains(reasons, "completion_status_blocked:required_edit_paths_remaining") {
		t.Fatalf("expected completion_status blocked trigger, got %#v", reasons)
	}
	if !auditWatchHighRiskStopReason("completion_gate_required_paths_remaining_repeated") {
		t.Fatalf("expected required-path completion stop reason to be high risk")
	}
}

func TestAuditWatchTriggersOnRecoveryEvents(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "recovery.jsonl")
	writeAuditWatchFile(t, logPath, strings.Join([]string{
		`{"type":"idiom.plan_execute.recovery_failure_record_written","session_id":"session-recovery","failure_id":"plan-execute-session-recovery","failure_class":"executor_edit_failed","failure_kind":"executor_failed","record_path":".idiom/recovery/plan-execute-session-recovery/failure.json","next_valid_action":"review_decomposition","summary":"RAW_SENTINEL"}`,
		`{"type":"idiom.plan_execute.recovery_decomposition_packet_written","session_id":"session-recovery","failure_id":"plan-execute-session-recovery","packet_id":"packet-plan-execute-session-recovery","packet_path":".idiom/recovery/plan-execute-session-recovery/packet.json","candidate_atoms":2,"next_valid_action":"review_decomposition","raw_prompt":"RAW_SENTINEL"}`,
		"",
	}, "\n"))
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat recovery log: %v", err)
	}
	trigger, ok := auditWatchTriggerForFile(auditWatchFile{
		Path:        logPath,
		DisplayPath: "logs/telemetry/recovery.jsonl",
		ModUnixNano: info.ModTime().UnixNano(),
		Size:        info.Size(),
	}, auditWatchSeenFile{})
	if !ok {
		t.Fatal("expected recovery trigger")
	}
	if !slicesContains(trigger.Reasons, "recovery_failure_record_written:executor_edit_failed") || !slicesContains(trigger.Reasons, "recovery_decomposition_packet_written") {
		t.Fatalf("expected recovery reasons, got %#v", trigger.Reasons)
	}
	if len(trigger.RecoveryEvents) != 2 {
		t.Fatalf("expected bounded recovery events, got %#v", trigger.RecoveryEvents)
	}

	review := auditWatchReview{TriggerFiles: []auditWatchTriggerFile{trigger}}
	auditWatchFinalizeReview(&review, auditWatchOptions{Limit: 5}, root)
	if review.Recovery == nil {
		t.Fatalf("expected recovery summary, got %#v", review)
	}
	if review.Recovery.Status != "ready" || review.Recovery.FailureRecordCount != 1 || review.Recovery.DecompositionPacketCount != 1 || review.Recovery.CandidateAtomCount != 2 {
		t.Fatalf("unexpected recovery summary: %#v", review.Recovery)
	}
	if !slicesContains(review.Recovery.FailureClasses, "executor_edit_failed") || !slicesContains(review.Recovery.NextValidActions, "review_decomposition") {
		t.Fatalf("expected recovery classes and actions, got %#v", review.Recovery)
	}
	if len(review.Findings) == 0 || review.Findings[0].Category != "failure_recovery" || review.Findings[0].OwnerSurface != "recovery" {
		t.Fatalf("expected failure recovery finding, got %#v", review.Findings)
	}
	if !slicesContains(review.Findings[0].Promotion.CandidateTargets, "recovery_atom") {
		t.Fatalf("expected recovery promotion target, got %#v", review.Findings[0].Promotion)
	}
	data, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal recovery review: %v", err)
	}
	if strings.Contains(string(data), "RAW_SENTINEL") || strings.Contains(string(data), root) {
		t.Fatalf("recovery audit packet leaked raw log content or root: %s", string(data))
	}
}

func TestAuditWatchFailedRunsGetPromotionAndClosureWorkflow(t *testing.T) {
	reasons := auditWatchTriggerReasons(map[string]any{
		"type":      "process_exit",
		"command":   "implement",
		"exit_code": float64(1),
	})
	if !slicesContains(reasons, "implement_process_failed") {
		t.Fatalf("expected failed implement trigger, got %#v", reasons)
	}
	findings := auditWatchFindingsFromTriggers([]auditWatchTriggerFile{{
		Path:       "logs/telemetry/failed.jsonl",
		Reasons:    reasons,
		EventCount: 1,
	}}, auditWatchOptions{Limit: 7}, "")
	if len(findings) != 1 {
		t.Fatalf("expected one failed-run finding, got %#v", findings)
	}
	finding := findings[0]
	if finding.Severity != "high" || finding.OwnerSurface != "telemetry" || finding.Status != "open" {
		t.Fatalf("failed run should be high/open/owned, got %#v", finding)
	}
	if !slicesContains(finding.Promotion.CandidateTargets, "regression_test") || !slicesContains(finding.Promotion.CandidateTargets, "eval_fixture") || !slicesContains(finding.Promotion.CandidateTargets, "telemetry_or_audit_rule") {
		t.Fatalf("failed run should carry promotion options, got %#v", finding.Promotion)
	}
	if finding.Closure.Ready || finding.Closure.Reason != "before_after_replay_required" {
		t.Fatalf("failed run should remain open until replay closure, got %#v", finding.Closure)
	}
}

func TestAuditWatchClosureRequiresBeforeAfterReplayOrBoundedReason(t *testing.T) {
	missingReplay := auditWatchClosureForStatus("fixed", nil, nil, "")
	if missingReplay.Ready || missingReplay.Status != "open" || missingReplay.Reason != "closed_status_requires_before_after_replay_or_bounded_reason" {
		t.Fatalf("fixed without replay should not close: %#v", missingReplay)
	}

	before := []auditWatchReplayEvidence{{Command: "go run ./cmd/idiom-eval -log before.jsonl", Result: "failed"}}
	after := []auditWatchReplayEvidence{{Command: "go run ./cmd/idiom-eval -log after.jsonl", Result: "passed"}}
	withReplay := auditWatchClosureForStatus("fixed", before, after, "")
	if !withReplay.Ready || withReplay.Status != "closed" || withReplay.Reason != "before_after_replay_present" {
		t.Fatalf("fixed with before/after replay should close: %#v", withReplay)
	}

	withBoundedReason := auditWatchClosureForStatus("declined", nil, nil, "documented_non_goal")
	if !withBoundedReason.Ready || withBoundedReason.Status != "closed" || withBoundedReason.Reason != "replay_not_applicable_documented" {
		t.Fatalf("declined with bounded no-replay reason should close: %#v", withBoundedReason)
	}

	withUnboundedReason := auditWatchClosureForStatus("declined", nil, nil, "seems fine")
	if withUnboundedReason.Ready || withUnboundedReason.Status != "open" {
		t.Fatalf("declined with unbounded no-replay reason should not close: %#v", withUnboundedReason)
	}
}

func TestAuditWatchOnlyScansAppendedLogContentAfterReview(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	telemetryPath := filepath.Join(root, "logs", "telemetry", "feedback.jsonl")
	writeAuditWatchFile(t, telemetryPath, `{"type":"tool_result","tool":"harness_feedback","summary":"Recorded feedback","error":""}`+"\n")
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{
		"watch",
		"--config", configPath,
		"--paths", "logs/telemetry",
		"--output", outputDir,
		"--once",
		"--review-existing",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("initial audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if reviews := auditWatchReviewFiles(t, outputDir); len(reviews) != 1 {
		t.Fatalf("expected initial review artifact, got %#v", reviews)
	}

	f, err := os.OpenFile(telemetryPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open telemetry append: %v", err)
	}
	if _, err := f.WriteString(`{"type":"process_exit","command":"tool:pwd","exit_code":0}` + "\n"); err != nil {
		t.Fatalf("append telemetry: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close telemetry append: %v", err)
	}
	nextModTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(telemetryPath, nextModTime, nextModTime); err != nil {
		t.Fatalf("touch telemetry: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = RunMain([]string{
		"watch",
		"--config", configPath,
		"--paths", "logs/telemetry",
		"--output", outputDir,
		"--once",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "no triggers") {
		t.Fatalf("expected appended non-trigger record to stay quiet, got %q", stdout.String())
	}
	if reviews := auditWatchReviewFiles(t, outputDir); len(reviews) != 1 {
		t.Fatalf("expected no duplicate review artifacts, got %#v", reviews)
	}
}

func TestAuditWatchHelpReturnsCleanly(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"watch", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected clean help exit, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "idiom audit watch") {
		t.Fatalf("expected audit watch usage, got stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestAuditWatchHelpSkipsFlagValues(t *testing.T) {
	if auditWatchHelpRequested([]string{"--output", "help"}) {
		t.Fatal("output directory named help should not request command help")
	}
	if !auditWatchHelpRequested([]string{"--output", "tmp", "--help"}) {
		t.Fatal("expected help flag after output value to request command help")
	}
}

func TestCollectAuditWatchFilesIncludesCaseInsensitiveJSONL(t *testing.T) {
	root := t.TempDir()
	writeAuditWatchFile(t, filepath.Join(root, "logs", "telemetry", "upper.JSONL"), "{}\n")
	writeAuditWatchFile(t, filepath.Join(root, "logs", "telemetry", "skip.txt"), "{}\n")

	files, err := collectAuditWatchFiles(root, []string{"logs/telemetry"})
	if err != nil {
		t.Fatalf("collectAuditWatchFiles returned error: %v", err)
	}
	if len(files) != 1 || files[0].DisplayPath != "logs/telemetry/upper.JSONL" {
		t.Fatalf("expected uppercase JSONL log to be collected, got %#v", files)
	}
}

func writeAuditWatchConfig(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "config.json")
	body := `{"workspace":{"root":` + strconvQuote(root) + `,"cwd":` + strconvQuote(root) + `,"enable_shell":true,"enable_writes":true}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeAuditWatchFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func auditWatchReviewFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "state.json" || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func auditWatchReviewHasClientClassification(review auditWatchReview, want string) bool {
	for _, row := range review.ClientClassifications {
		if row.ID == want && row.Status == "covered" && row.ExampleCheck != "" {
			return true
		}
	}
	return false
}

func auditWatchReviewHasProductGateImpact(review auditWatchReview, want string) bool {
	for _, impact := range review.ProductSuccessGateImpacts {
		if impact.Gate == want && impact.Status == "strengthened" {
			return true
		}
	}
	return false
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
