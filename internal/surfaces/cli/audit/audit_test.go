package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ = ginkgo.Describe("audit", func() {
	ginkgo.It("audit watch once writes review for feedback telemetry", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY", "0")

		root := ginkgo.GinkgoT().TempDir()
		configPath := writeAuditWatchConfig(root)
		telemetryDir := filepath.Join(root, "logs", "telemetry")
		gomega.Expect(os.MkdirAll(telemetryDir, 0o755)).To(gomega.Succeed())

		writeAuditWatchFile(filepath.Join(telemetryDir, "feedback.jsonl"), strings.Join([]string{
			`{"type":"session_start","command":"tool:harness_feedback","workspace_root":"` + root + `"}`,
			`{"type":"tool_result","tool":"harness_feedback","summary":"Recorded workflow_gap feedback for completion_gate","error":"","feedback_category":"workflow_gap","feedback_component":"completion_gate","feedback_confidence":"verified","feedback_severity":"medium"}`,
			`{"type":"process_exit","command":"tool:harness_feedback","exit_code":0}`,
			"",
		}, "\n"))

		outputDir := filepath.Join(root, ".idiom", "audits", "watch")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"watch",
			"--config", configPath,
			"--paths", "logs/telemetry",
			"--output", outputDir,
			"--once",
			"--review-existing",
			"--debounce", "0",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("audit review:"), fmt.Sprintf("expected review path on stdout, got %q", stdout.String()))

		reviews := auditWatchReviewFiles(outputDir)
		gomega.Expect(reviews).To(gomega.HaveLen(1), fmt.Sprintf("expected one review artifact, got %#v", reviews))

		var review auditWatchReview

		data, err := os.ReadFile(reviews[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read review: %v", err))
		gomega.Expect(json.Unmarshal(data, &review)).To(gomega.Succeed())
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring(root), fmt.Sprintf("review packet leaked raw workspace root: %s", string(data)))
		gomega.Expect(review.SchemaVersion).To(gomega.Equal(auditWatchPacketSchemaVersion), fmt.Sprintf("expected audit-watch packet schema, got %q", review.SchemaVersion))
		gomega.Expect(review.Status).To(gomega.Equal("open"), fmt.Sprintf("expected open workflow packet, status=%q promotion=%q", review.Status, review.PromotionStatus))
		gomega.Expect(review.PromotionStatus).To(gomega.Equal("open"), fmt.Sprintf("expected open workflow packet, status=%q promotion=%q", review.Status, review.PromotionStatus))
		gomega.Expect(review.OwnerSurface).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected owner surface and failure class, got owner=%q top=%q", review.OwnerSurface, review.TopFailureClass))
		gomega.Expect(review.TopFailureClass).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected owner surface and failure class, got owner=%q top=%q", review.OwnerSurface, review.TopFailureClass))
		gomega.Expect(review.TriggerFiles).To(gomega.HaveLen(1), fmt.Sprintf("expected harness_feedback trigger, got %#v", review.TriggerFiles))
		gomega.Expect(review.TriggerFiles[0].Reasons).To(gomega.ContainElement("harness_feedback"), fmt.Sprintf("expected harness_feedback trigger, got %#v", review.TriggerFiles))
		gomega.Expect(review.Audits).To(gomega.HaveLen(1), fmt.Sprintf("expected embedded harness audit report, got %#v", review.Audits))
		gomega.Expect(review.Audits[0].Report).NotTo(gomega.BeNil(), fmt.Sprintf("expected embedded harness audit report, got %#v", review.Audits))
		gomega.Expect(review.Findings).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected owned findings from trigger/audit evidence, got %#v", review))

		for _, finding := range review.Findings {
			if finding.Severity != "high" && finding.Severity != "medium" {
				continue
			}
			gomega.Expect(finding.OwnerSurface).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding missing owner/status/reproducer: %#v", finding))
			gomega.Expect(finding.Status).To(gomega.Equal("open"), fmt.Sprintf("finding missing owner/status/reproducer: %#v", finding))
			gomega.Expect(finding.Reproducer.AuditCommand).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding missing owner/status/reproducer: %#v", finding))
			gomega.Expect(finding.Promotion.CandidateTargets).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding missing regression-promotion path: %#v", finding))
			gomega.Expect(finding.Promotion.RequiredDecision).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding missing regression-promotion path: %#v", finding))
			gomega.Expect(finding.Closure.Ready).To(gomega.BeFalse(), fmt.Sprintf("open finding should require replay closure evidence: %#v", finding.Closure))
			gomega.Expect(finding.Closure.Reason).To(gomega.Equal("before_after_replay_required"), fmt.Sprintf("open finding should require replay closure evidence: %#v", finding.Closure))

		}
		gomega.Expect(slicesContains(review.CandidateRegressions, "regression-trigger-harness-feedback")).To(gomega.BeTrue(), fmt.Sprintf("expected trigger candidate regression, got %#v", review.CandidateRegressions))
		gomega.Expect(review.ClientClassifications).To(gomega.HaveLen(7), fmt.Sprintf("expected all client classifications, got %#v", review.ClientClassifications))

		for _, want := range []string{"finance", "healthcare_life_sciences", "public_sector", "enterprise_saas", "tooling_vendor", "local_model_private_code", "open_source"} {
			gomega.Expect(auditWatchReviewHasClientClassification(review, want)).To(gomega.BeTrue(), fmt.Sprintf("missing client classification %q in %#v", want, review.ClientClassifications))

		}

		for _, want := range []string{"security_privacy", "retained_use", "integration_proof", "client_demo_proof", "packaging_support", "local_product_evidence_export"} {
			gomega.Expect(auditWatchReviewHasProductGateImpact(review, want)).To(gomega.BeTrue(), fmt.Sprintf("missing product-success gate impact %q in %#v", want, review.ProductSuccessGateImpacts))

		}
		gomega.Expect(review.Privacy.EvidencePolicy).To(gomega.Equal("bounded_audit_metadata_only"), fmt.Sprintf("expected bounded privacy policy, got %#v", review.Privacy))
		gomega.Expect(review.Privacy.PromptTextIncluded).To(gomega.BeFalse(), fmt.Sprintf("expected bounded privacy policy, got %#v", review.Privacy))
		gomega.Expect(review.Privacy.FileBodiesIncluded).To(gomega.BeFalse(), fmt.Sprintf("expected bounded privacy policy, got %#v", review.Privacy))
		gomega.Expect(review.Privacy.LogExcerptsIncluded).To(gomega.BeFalse(), fmt.Sprintf("expected bounded privacy policy, got %#v", review.Privacy))
		gomega.Expect(review.Privacy.SecretsIncluded).To(gomega.BeFalse(), fmt.Sprintf("expected bounded privacy policy, got %#v", review.Privacy))

	})
	ginkgo.It("audit watch sanitizes sensitive trigger session and reproducer evidence", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY", "0")

		root := ginkgo.GinkgoT().TempDir()
		configPath := writeAuditWatchConfig(root)
		telemetryDir := filepath.Join(root, "logs", "telemetry", "token=SECRET")
		gomega.Expect(os.MkdirAll(telemetryDir, 0o755)).To(gomega.Succeed())

		writeAuditWatchFile(filepath.Join(telemetryDir, "feedback.jsonl"), strings.Join([]string{
			`{"type":"tool_result","tool":"harness_feedback","session_id":"token=SESSIONSECRET","summary":"Recorded workflow_gap feedback","error":""}`,
			`{"type":"completion_status","status":"blocked","reason":"token=BLOCKSECRET","session_id":"token=SESSIONSECRET"}`,
			"",
		}, "\n"))

		outputDir := filepath.Join(root, ".idiom", "audits", "watch")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"watch",
			"--config", configPath,
			"--paths", "logs/telemetry/token=SECRET",
			"--output", outputDir,
			"--once",
			"--review-existing",
			"--debounce", "0",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		reviews := auditWatchReviewFiles(outputDir)
		gomega.Expect(reviews).To(gomega.HaveLen(1), fmt.Sprintf("expected one review artifact, got %#v stdout=%s", reviews, stdout.String()))

		data, err := os.ReadFile(reviews[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read review: %v", err))

		packet := string(data)
		for _, forbidden := range []string{root, "token=SECRET", "token=SESSIONSECRET", "token=BLOCKSECRET", "SESSIONSECRET", "BLOCKSECRET"} {
			gomega.Expect(packet).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("audit-watch packet leaked %q: %s", forbidden, packet))

		}

		var review auditWatchReview
		gomega.Expect(json.Unmarshal(data, &review)).To(gomega.Succeed())
		gomega.Expect(review.SchemaVersion).To(gomega.Equal(auditWatchPacketSchemaVersion), fmt.Sprintf("expected open schema-versioned packet, got schema=%q status=%q promotion=%q", review.SchemaVersion, review.Status, review.PromotionStatus))
		gomega.Expect(review.Status).To(gomega.Equal("open"), fmt.Sprintf("expected open schema-versioned packet, got schema=%q status=%q promotion=%q", review.SchemaVersion, review.Status, review.PromotionStatus))
		gomega.Expect(review.PromotionStatus).To(gomega.Equal("open"), fmt.Sprintf("expected open schema-versioned packet, got schema=%q status=%q promotion=%q", review.SchemaVersion, review.Status, review.PromotionStatus))
		gomega.Expect(review.TriggerFiles).To(gomega.HaveLen(1), fmt.Sprintf("expected redacted trigger path, got %#v", review.TriggerFiles))
		gomega.Expect(review.TriggerFiles[0].Path).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected redacted trigger path, got %#v", review.TriggerFiles))
		gomega.Expect(review.TriggerFiles[0].Path).To(gomega.ContainSubstring("redacted"), fmt.Sprintf("expected redacted trigger path, got %#v", review.TriggerFiles))
		gomega.Expect(review.TriggerFiles[0].Sessions).To(gomega.HaveLen(1), fmt.Sprintf("expected redacted trigger session, got %#v", review.TriggerFiles[0].Sessions))
		gomega.Expect(review.TriggerFiles[0].Sessions[0]).To(gomega.ContainSubstring("redacted"), fmt.Sprintf("expected redacted trigger session, got %#v", review.TriggerFiles[0].Sessions))
		gomega.Expect(slicesContains(review.TriggerFiles[0].Reasons, "completion_status_blocked:redacted")).To(gomega.BeTrue(), fmt.Sprintf("expected sensitive completion reason to be bounded, got %#v", review.TriggerFiles[0].Reasons))
		gomega.Expect(review.Reproduce.Evidence).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected redacted packet reproducer evidence, got %#v", review.Reproduce))
		gomega.Expect(strings.Join(review.Reproduce.Evidence, " ")).To(gomega.ContainSubstring("redacted"), fmt.Sprintf("expected redacted packet reproducer evidence, got %#v", review.Reproduce))
		gomega.Expect(review.Findings).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected findings to remain present: %#v", review))

		for _, finding := range review.Findings {
			gomega.Expect(finding.OwnerSurface).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding lost workflow fields after sanitization: %#v", finding))
			gomega.Expect(finding.Status).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding lost workflow fields after sanitization: %#v", finding))
			gomega.Expect(finding.Reproducer.AuditCommand).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding lost workflow fields after sanitization: %#v", finding))
			gomega.Expect(finding.Promotion.CandidateTargets).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding lost workflow fields after sanitization: %#v", finding))
			gomega.Expect(finding.Closure.Reason).NotTo(gomega.BeEmpty(), fmt.Sprintf("finding lost workflow fields after sanitization: %#v", finding))

		}
		gomega.Expect(review.ClientClassifications).To(gomega.HaveLen(7), fmt.Sprintf("expected all client classifications, got %#v", review.ClientClassifications))

		for _, want := range []string{"security_privacy", "retained_use", "integration_proof", "client_demo_proof", "packaging_support", "local_product_evidence_export"} {
			gomega.Expect(auditWatchReviewHasProductGateImpact(review, want)).To(gomega.BeTrue(), fmt.Sprintf("missing product-success gate impact %q in %#v", want, review.ProductSuccessGateImpacts))

		}
	})
	ginkgo.It("audit watch sanitizes token credential json keys", func() {
		value := map[string]any{
			"access_token":  "sk-live-value",
			"authorization": "Bearer live-value",
			"credential":    "plain-value",
			"client_secret": "client-live-value",
			"nested": map[string]any{
				"private_key": "-----BEGIN PRIVATE KEY-----",
			},
		}

		sanitized := auditWatchSanitizeJSONValue("", value).(map[string]any)
		for _, key := range []string{"access_token", "authorization", "credential", "client_secret"} {
			if got := sanitized[key]; got != "[redacted]" {
				ginkgo.Fail(fmt.Sprintf("%s was not redacted: %#v", key, got))
			}
		}
		nested := sanitized["nested"].(map[string]any)
		if got := nested["private_key"]; got != "[redacted]" {
			ginkgo.Fail(fmt.Sprintf("private_key was not redacted: %#v", got))
		}
	})
	ginkgo.It("audit watch sanitizes customer and raw transcript evidence", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY", "0")

		root := ginkgo.GinkgoT().TempDir()
		configPath := writeAuditWatchConfig(root)
		telemetryDir := filepath.Join(root, "logs", "telemetry", "secrets", "customer-acme")
		gomega.Expect(os.MkdirAll(telemetryDir, 0o755)).To(gomega.Succeed())

		writeAuditWatchFile(filepath.Join(telemetryDir, "feedback.jsonl"), strings.Join([]string{
			`{"type":"tool_result","tool":"harness_feedback","session_id":"customer-acme-session","summary":"Recorded workflow_gap feedback","error":""}`,
			`{"type":"completion_status","status":"blocked","reason":"raw_prompt patient transcript body","session_id":"raw_prompt customer transcript"}`,
			"",
		}, "\n"))

		outputDir := filepath.Join(root, ".idiom", "audits", "watch")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"watch",
			"--config", configPath,
			"--paths", "logs/telemetry/secrets/customer-acme",
			"--output", outputDir,
			"--once",
			"--review-existing",
			"--debounce", "0",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		reviews := auditWatchReviewFiles(outputDir)
		gomega.Expect(reviews).To(gomega.HaveLen(1), fmt.Sprintf("expected one review artifact, got %#v stdout=%s", reviews, stdout.String()))

		data, err := os.ReadFile(reviews[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read review: %v", err))

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
			gomega.Expect(packet).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("audit-watch packet leaked %q: %s", forbidden, packet))

		}

		var review auditWatchReview
		gomega.Expect(json.Unmarshal(data, &review)).To(gomega.Succeed())
		gomega.Expect(review.TriggerFiles).To(gomega.HaveLen(1), fmt.Sprintf("expected one trigger file, got %#v", review.TriggerFiles))

		trigger := review.TriggerFiles[0]
		gomega.Expect(trigger.Path).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected sensitive trigger path to be redacted, got %#v", trigger))
		gomega.Expect(trigger.Path).To(gomega.ContainSubstring("redacted_path"), fmt.Sprintf("expected sensitive trigger path to be redacted, got %#v", trigger))
		gomega.Expect(trigger.Sessions).To(gomega.HaveLen(2), fmt.Sprintf("expected both sessions to remain as bounded evidence, got %#v", trigger.Sessions))

		joinedSessions := strings.Join(trigger.Sessions, " ")
		gomega.Expect(joinedSessions).To(gomega.ContainSubstring("redacted_customer"), fmt.Sprintf("expected customer and raw-content sessions to be bounded, got %#v", trigger.Sessions))
		gomega.Expect(joinedSessions).To(gomega.ContainSubstring("redacted_raw-content"), fmt.Sprintf("expected customer and raw-content sessions to be bounded, got %#v", trigger.Sessions))
		gomega.Expect(trigger.Reasons).To(gomega.ContainElements("harness_feedback", "completion_status_blocked:redacted"), fmt.Sprintf("expected useful bounded reasons to remain, got %#v", trigger.Reasons))
		gomega.Expect(review.Reproduce.Evidence).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected redacted packet reproducer evidence, got %#v", review.Reproduce))
		gomega.Expect(strings.Join(review.Reproduce.Evidence, " ")).To(gomega.ContainSubstring("redacted_path"), fmt.Sprintf("expected redacted packet reproducer evidence, got %#v", review.Reproduce))
		gomega.Expect(review.Findings).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected findings and client classifications to remain present: findings=%#v clients=%#v", review.Findings, review.ClientClassifications))
		gomega.Expect(review.ClientClassifications).To(gomega.HaveLen(7), fmt.Sprintf("expected findings and client classifications to remain present: findings=%#v clients=%#v", review.Findings, review.ClientClassifications))

	})
	ginkgo.It("audit watch first run baselines without review existing", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY", "0")

		root := ginkgo.GinkgoT().TempDir()
		configPath := writeAuditWatchConfig(root)
		telemetryDir := filepath.Join(root, "logs", "telemetry")
		gomega.Expect(os.MkdirAll(telemetryDir, 0o755)).To(gomega.Succeed())

		writeAuditWatchFile(filepath.Join(telemetryDir, "feedback.jsonl"), `{"type":"tool_result","tool":"harness_feedback","summary":"Recorded feedback","error":""}`+"\n")

		outputDir := filepath.Join(root, ".idiom", "audits", "watch")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"watch",
			"--config", configPath,
			"--paths", "logs/telemetry",
			"--output", outputDir,
			"--once",
			"--debounce", "0",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("no triggers"), fmt.Sprintf("expected baseline no-trigger status, got %q", stdout.String()))

		if reviews := auditWatchReviewFiles(outputDir); len(reviews) != 0 {
			ginkgo.Fail(fmt.Sprintf("expected no review artifacts, got %#v", reviews))
		}

		if _, err := os.Stat(filepath.Join(outputDir, "state.json")); err != nil {
			ginkgo.Fail(fmt.Sprintf("expected state file: %v", err))
		}
	})
	ginkgo.It("audit watch default paths include conversation logs", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY", "0")

		root := ginkgo.GinkgoT().TempDir()
		configPath := writeAuditWatchConfig(root)
		writeAuditWatchFile(filepath.Join(root, "logs", "conversations", "completion.jsonl"), strings.Join([]string{
			`{"type":"tool_result","tool":"completion_gate","summary":"Mutation completion gate rejected final answer","error":"no successful write tool result is present"}`,
			"",
		}, "\n"))

		outputDir := filepath.Join(root, ".idiom", "audits", "watch")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"watch",
			"--config", configPath,
			"--output", outputDir,
			"--once",
			"--review-existing",
			"--debounce", "0",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		reviews := auditWatchReviewFiles(outputDir)
		gomega.Expect(reviews).To(gomega.HaveLen(1), fmt.Sprintf("expected one review artifact from default logs/conversations path, got %#v stdout=%s", reviews, stdout.String()))

		var review auditWatchReview

		data, err := os.ReadFile(reviews[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read review: %v", err))
		gomega.Expect(json.Unmarshal(data, &review)).To(gomega.Succeed())
		gomega.Expect(review.TriggerFiles).To(gomega.HaveLen(1), fmt.Sprintf("expected trigger from logs/conversations, got %#v", review.TriggerFiles))
		gomega.Expect(review.TriggerFiles[0].Path).To(gomega.ContainSubstring("logs/conversations"), fmt.Sprintf("expected trigger from logs/conversations, got %#v", review.TriggerFiles))
		gomega.Expect(review.Audits).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected embedded harness audits for default paths"))

	})
	ginkgo.It("audit watch triggers on blocked completion status", func() {
		reasons := auditWatchTriggerReasons(map[string]any{
			"type":         "completion_status",
			"status":       "blocked",
			"reason":       "required_edit_paths_remaining",
			"blocker_kind": "required_edit_paths_remaining",
		})
		gomega.Expect(slicesContains(reasons, "completion_status_blocked:required_edit_paths_remaining")).To(gomega.BeTrue(), fmt.Sprintf("expected completion_status blocked trigger, got %#v", reasons))
		gomega.Expect(auditWatchHighRiskStopReason("completion_gate_required_paths_remaining_repeated")).To(gomega.BeTrue(), fmt.Sprintf("expected required-path completion stop reason to be high risk"))

	})
	ginkgo.It("audit watch triggers on recovery events", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "recovery.jsonl")
		writeAuditWatchFile(logPath, strings.Join([]string{
			`{"type":"idiom.plan_execute.recovery_failure_record_written","session_id":"session-recovery","failure_id":"plan-execute-session-recovery","failure_class":"executor_edit_failed","failure_kind":"executor_failed","record_path":".idiom/recovery/plan-execute-session-recovery/failure.json","next_valid_action":"review_decomposition","summary":"RAW_SENTINEL"}`,
			`{"type":"idiom.plan_execute.recovery_decomposition_packet_written","session_id":"session-recovery","failure_id":"plan-execute-session-recovery","packet_id":"packet-plan-execute-session-recovery","packet_path":".idiom/recovery/plan-execute-session-recovery/packet.json","candidate_atoms":2,"next_valid_action":"review_decomposition","raw_prompt":"RAW_SENTINEL"}`,
			"",
		}, "\n"))

		info, err := os.Stat(logPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("stat recovery log: %v", err))

		trigger, ok := auditWatchTriggerForFile(auditWatchFile{
			Path:        logPath,
			DisplayPath: "logs/telemetry/recovery.jsonl",
			ModUnixNano: info.ModTime().UnixNano(),
			Size:        info.Size(),
		}, auditWatchSeenFile{})
		gomega.Expect(ok).To(gomega.BeTrue(), "expected recovery trigger")
		gomega.Expect(trigger.Reasons).To(gomega.ContainElements("recovery_failure_record_written:executor_edit_failed", "recovery_decomposition_packet_written"), fmt.Sprintf("expected recovery reasons, got %#v", trigger.Reasons))
		gomega.Expect(trigger.RecoveryEvents).To(gomega.HaveLen(2), fmt.Sprintf("expected bounded recovery events, got %#v", trigger.RecoveryEvents))

		review := auditWatchReview{TriggerFiles: []auditWatchTriggerFile{trigger}}
		auditWatchFinalizeReview(&review, auditWatchOptions{Limit: 5}, root)
		gomega.Expect(review.Recovery).NotTo(gomega.BeNil(), fmt.Sprintf("expected recovery summary, got %#v", review))
		gomega.Expect(review.Recovery.Status).To(gomega.Equal("ready"), fmt.Sprintf("unexpected recovery summary: %#v", review.Recovery))
		gomega.Expect(review.Recovery.FailureRecordCount).To(gomega.Equal(1), fmt.Sprintf("unexpected recovery summary: %#v", review.Recovery))
		gomega.Expect(review.Recovery.DecompositionPacketCount).To(gomega.Equal(1), fmt.Sprintf("unexpected recovery summary: %#v", review.Recovery))
		gomega.Expect(review.Recovery.CandidateAtomCount).To(gomega.Equal(2), fmt.Sprintf("unexpected recovery summary: %#v", review.Recovery))
		gomega.Expect(review.Recovery.FailureClasses).To(gomega.ContainElement("executor_edit_failed"), fmt.Sprintf("expected recovery classes and actions, got %#v", review.Recovery))
		gomega.Expect(review.Recovery.NextValidActions).To(gomega.ContainElement("review_decomposition"), fmt.Sprintf("expected recovery classes and actions, got %#v", review.Recovery))
		gomega.Expect(review.Findings).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected failure recovery finding, got %#v", review.Findings))
		gomega.Expect(review.Findings[0].Category).To(gomega.Equal("failure_recovery"), fmt.Sprintf("expected failure recovery finding, got %#v", review.Findings))
		gomega.Expect(review.Findings[0].OwnerSurface).To(gomega.Equal("recovery"), fmt.Sprintf("expected failure recovery finding, got %#v", review.Findings))
		gomega.Expect(slicesContains(review.Findings[0].Promotion.CandidateTargets, "recovery_atom")).To(gomega.BeTrue(), fmt.Sprintf("expected recovery promotion target, got %#v", review.Findings[0].Promotion))

		data, err := json.Marshal(review)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal recovery review: %v", err))
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring("RAW_SENTINEL"), fmt.Sprintf("recovery audit packet leaked raw log content or root: %s", string(data)))
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring(root), fmt.Sprintf("recovery audit packet leaked raw log content or root: %s", string(data)))

	})
	ginkgo.It("audit watch failed runs get promotion and closure workflow", func() {
		reasons := auditWatchTriggerReasons(map[string]any{
			"type":      "process_exit",
			"command":   "implement",
			"exit_code": float64(1),
		})
		gomega.Expect(slicesContains(reasons, "implement_process_failed")).To(gomega.BeTrue(), fmt.Sprintf("expected failed implement trigger, got %#v", reasons))

		findings := auditWatchFindingsFromTriggers([]auditWatchTriggerFile{{
			Path:       "logs/telemetry/failed.jsonl",
			Reasons:    reasons,
			EventCount: 1,
		}}, auditWatchOptions{Limit: 7}, "")
		gomega.Expect(findings).To(gomega.HaveLen(1), fmt.Sprintf("expected one failed-run finding, got %#v", findings))

		finding := findings[0]
		gomega.Expect(finding.Severity).To(gomega.Equal("high"), fmt.Sprintf("failed run should be high/open/owned, got %#v", finding))
		gomega.Expect(finding.OwnerSurface).To(gomega.Equal("telemetry"), fmt.Sprintf("failed run should be high/open/owned, got %#v", finding))
		gomega.Expect(finding.Status).To(gomega.Equal("open"), fmt.Sprintf("failed run should be high/open/owned, got %#v", finding))
		gomega.Expect(finding.Promotion.CandidateTargets).To(gomega.ContainElements("regression_test", "eval_fixture", "telemetry_or_audit_rule"), fmt.Sprintf("failed run should carry promotion options, got %#v", finding.Promotion))
		gomega.Expect(finding.Closure.Ready).To(gomega.BeFalse(), fmt.Sprintf("failed run should remain open until replay closure, got %#v", finding.Closure))
		gomega.Expect(finding.Closure.Reason).To(gomega.Equal("before_after_replay_required"), fmt.Sprintf("failed run should remain open until replay closure, got %#v", finding.Closure))

	})
	ginkgo.It("audit watch closure requires before after replay or bounded reason", func() {
		missingReplay := auditWatchClosureForStatus("fixed", nil, nil, "")
		gomega.Expect(missingReplay.Ready).To(gomega.BeFalse(), fmt.Sprintf("fixed without replay should not close: %#v", missingReplay))
		gomega.Expect(missingReplay.Status).To(gomega.Equal("open"), fmt.Sprintf("fixed without replay should not close: %#v", missingReplay))
		gomega.Expect(missingReplay.Reason).To(gomega.Equal("closed_status_requires_before_after_replay_or_bounded_reason"), fmt.Sprintf("fixed without replay should not close: %#v", missingReplay))

		before := []auditWatchReplayEvidence{{Command: "go run ./cmd/idiom-eval -log before.jsonl", Result: "failed"}}
		after := []auditWatchReplayEvidence{{Command: "go run ./cmd/idiom-eval -log after.jsonl", Result: "passed"}}
		withReplay := auditWatchClosureForStatus("fixed", before, after, "")
		gomega.Expect(withReplay.Ready).To(gomega.BeTrue(), fmt.Sprintf("fixed with before/after replay should close: %#v", withReplay))
		gomega.Expect(withReplay.Status).To(gomega.Equal("closed"), fmt.Sprintf("fixed with before/after replay should close: %#v", withReplay))
		gomega.Expect(withReplay.Reason).To(gomega.Equal("before_after_replay_present"), fmt.Sprintf("fixed with before/after replay should close: %#v", withReplay))

		withBoundedReason := auditWatchClosureForStatus("declined", nil, nil, "documented_non_goal")
		gomega.Expect(withBoundedReason.Ready).To(gomega.BeTrue(), fmt.Sprintf("declined with bounded no-replay reason should close: %#v", withBoundedReason))
		gomega.Expect(withBoundedReason.Status).To(gomega.Equal("closed"), fmt.Sprintf("declined with bounded no-replay reason should close: %#v", withBoundedReason))
		gomega.Expect(withBoundedReason.Reason).To(gomega.Equal("replay_not_applicable_documented"), fmt.Sprintf("declined with bounded no-replay reason should close: %#v", withBoundedReason))

		withUnboundedReason := auditWatchClosureForStatus("declined", nil, nil, "seems fine")
		gomega.Expect(withUnboundedReason.Ready).To(gomega.BeFalse(), fmt.Sprintf("declined with unbounded no-replay reason should not close: %#v", withUnboundedReason))
		gomega.Expect(withUnboundedReason.Status).To(gomega.Equal("open"), fmt.Sprintf("declined with unbounded no-replay reason should not close: %#v", withUnboundedReason))

	})
	ginkgo.It("audit watch only scans appended log content after review", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY", "0")

		root := ginkgo.GinkgoT().TempDir()
		configPath := writeAuditWatchConfig(root)
		telemetryPath := filepath.Join(root, "logs", "telemetry", "feedback.jsonl")
		writeAuditWatchFile(telemetryPath, `{"type":"tool_result","tool":"harness_feedback","summary":"Recorded feedback","error":""}`+"\n")

		outputDir := filepath.Join(root, ".idiom", "audits", "watch")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"watch",
			"--config", configPath,
			"--paths", "logs/telemetry",
			"--output", outputDir,
			"--once",
			"--review-existing",
			"--debounce", "0",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("initial audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		if reviews := auditWatchReviewFiles(outputDir); len(reviews) != 1 {
			ginkgo.Fail(fmt.Sprintf("expected initial review artifact, got %#v", reviews))
		}

		f, err := os.OpenFile(telemetryPath, os.O_APPEND|os.O_WRONLY, 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open telemetry append: %v", err))

		if _, err := f.WriteString(`{"type":"process_exit","command":"tool:pwd","exit_code":0}` + "\n"); err != nil {
			ginkgo.Fail(fmt.Sprintf("append telemetry: %v", err))
		}
		gomega.Expect(f.Close()).To(gomega.Succeed())

		nextModTime := time.Now().Add(2 * time.Second)
		gomega.Expect(os.Chtimes(telemetryPath, nextModTime, nextModTime)).To(gomega.Succeed())

		stdout.Reset()
		stderr.Reset()

		code = RunMainWithTelemetry([]string{
			"watch",
			"--config", configPath,
			"--paths", "logs/telemetry",
			"--output", outputDir,
			"--once",
			"--debounce", "0",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("second audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("no triggers"), fmt.Sprintf("expected appended non-trigger record to stay quiet, got %q", stdout.String()))

		if reviews := auditWatchReviewFiles(outputDir); len(reviews) != 1 {
			ginkgo.Fail(fmt.Sprintf("expected no duplicate review artifacts, got %#v", reviews))
		}
	})
	ginkgo.It("audit watch help returns cleanly", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY", "0")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"watch", "--help"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected clean help exit, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("idiom audit watch"), fmt.Sprintf("expected audit watch usage, got stdout=%s stderr=%s", stdout.String(), stderr.String()))

	})
	ginkgo.It("audit watch help skips flag values", func() {
		gomega.Expect(auditWatchHelpRequested([]string{"--output", "help"})).To(gomega.BeFalse(), "output directory named help should not request command help")
		gomega.Expect(auditWatchHelpRequested([]string{"--output", "tmp", "--help"})).To(gomega.BeTrue(), "expected help flag after output value to request command help")

	})
	ginkgo.It("collect audit watch files includes case insensitive jsonl", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeAuditWatchFile(filepath.Join(root, "logs", "telemetry", "upper.JSONL"), "{}\n")
		writeAuditWatchFile(filepath.Join(root, "logs", "telemetry", "skip.txt"), "{}\n")

		files, err := collectAuditWatchFiles(root, []string{"logs/telemetry"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("collectAuditWatchFiles returned error: %v", err))
		gomega.Expect(files).To(gomega.HaveLen(1), fmt.Sprintf("expected uppercase JSONL log to be collected, got %#v", files))
		gomega.Expect(files[0].DisplayPath).To(gomega.Equal("logs/telemetry/upper.JSONL"), fmt.Sprintf("expected uppercase JSONL log to be collected, got %#v", files))

	})
})

func writeAuditWatchConfig(root string) string {

	path := filepath.Join(root, "config.json")
	body := `{"workspace":{"root":` + strconvQuote(root) + `,"cwd":` + strconvQuote(root) + `,"enable_shell":true,"enable_writes":true}}` + "\n"
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

	return path
}

func writeAuditWatchFile(path string, content string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())

}

func auditWatchReviewFiles(dir string) []string {

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", dir, err))

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
