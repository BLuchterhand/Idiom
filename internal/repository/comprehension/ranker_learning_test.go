package comprehension

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("apply verified ranker learning accepts verified evidence", func() {
	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"internal/runtime/agent/runner.go", "../escape.go", "internal/runtime/agent/runner.go"},
		Score:    0.91,
		TaskID:   "task-123",
		CorpusID: "corpus-1",
		ResultID: "result-1",
		Reason:   "swe_groundedness_oracle",
		Verified: true,
		Evidence: LearningEvidenceRef{
			Kind:     "swe_groundedness_oracle",
			Verified: true,
			Metrics:  map[string]float64{"boost_at": 0.75},
		},
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{"internal/runtime/agent/runner.go"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
			VerificationOutput:  "ok",
		},
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.EventID).NotTo(BeEquivalentTo(""), "event id missing: %#v", event)
	Expect(event.State).To(BeEquivalentTo(LearningStateAccepted), "event state/reason = %q/%q", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo("swe_groundedness_oracle"), "event state/reason = %q/%q", event.State, event.Reason)
	{
		got := weights["internal/runtime/agent/runner.go"]
		Expect(got <= LearnedWeightBaseline).To(BeFalse(), "expected boosted weight, got %v", got)
	}
	{
		_, ok := weights["../escape.go"]
		Expect(ok).To(BeFalse(), "invalid path should not be weighted: %v", weights)
	}
	Expect(event.Evidence).NotTo(BeNil(), "evidence should carry sanitized path details: %#v", event.Evidence)
	Expect(event.Evidence.InvalidPathCount).To(BeEquivalentTo(1), "evidence should carry sanitized path details: %#v", event.Evidence)
	Expect(len(event.Evidence.Files)).To(BeEquivalentTo(1), "evidence should carry sanitized path details: %#v", event.Evidence)
	Expect(event.BeforeWeights).NotTo(BeNil(), "event missing rollback data: %#v", event)
	Expect(event.AfterWeights).NotTo(BeNil(), "event missing rollback data: %#v", event)
	Expect(event.Rollback).NotTo(BeNil(), "event missing rollback data: %#v", event)

	entries, err := ReadLearningLog(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "read log: %v", err)
	Expect(len(entries)).To(BeEquivalentTo(1), "unexpected log entries: %#v", entries)
	Expect(entries[0].EventID).To(BeEquivalentTo(event.EventID), "unexpected log entries: %#v", entries)

	summary := SummarizeLearningLog(entries)
	Expect(summary.TotalEvents).To(BeEquivalentTo(1), "summary missing accepted event counts: %#v", summary)
	Expect(summary.AcceptedCount).To(BeEquivalentTo(1), "summary missing accepted event counts: %#v", summary)
	Expect(summary.AcceptedReasonCounts["swe_groundedness_oracle"]).To(BeEquivalentTo(1), "summary missing accepted event counts: %#v", summary)
	snapshot, err := LoadRankerStateSnapshot(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	auditSummary := snapshot.AuditSummary()
	Expect(auditSummary.ReasonCounts).To(BeEmpty())
	Expect(auditSummary.AcceptedReasonCounts).To(BeEmpty())
	Expect(auditSummary.Recent).To(HaveLen(1))
	Expect(auditSummary.Recent[0].Files).To(BeEmpty())
	Expect(auditSummary.Recent[0].BeforeWeights).To(BeNil())
	Expect(auditSummary.Recent[0].AfterWeights).To(BeNil())
	Expect(auditSummary.Recent[0].Verification).To(BeNil())
	Expect(auditSummary.Recent[0].EventID).To(BeEmpty())
})
var _ = It("apply verified ranker learning rejects unverified evidence", func() {
	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source: "eval",
		Action: "boost",
		Files:  []string{"internal/runtime/agent/runner.go"},
		Score:  0.9,
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.State).To(BeEquivalentTo(LearningStateRejected), "event state/reason = %q/%q", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonUnverified), "event state/reason = %q/%q", event.State, event.Reason)
	Expect(len(weights)).To(BeEquivalentTo(0), "unverified evidence must not update weights: %v", weights)
	{
		_, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "weights.json"))
		Expect(os.IsNotExist(err)).To(BeTrue(), "unverified evidence should not create weights.json, err=%v", err)
	}

	entries, _ := ReadLearningLog(GinkgoT().Context(), root)
	summary := SummarizeLearningLog(entries)
	Expect(summary.RejectedCount).To(BeEquivalentTo(1), "summary missing rejected reason counts: %#v", summary)
	Expect(summary.RejectedReasonCounts[LearningReasonUnverified]).To(BeEquivalentTo(1), "summary missing rejected reason counts: %#v", summary)
})
var _ = It("apply verified ranker learning log is privacy safe", func() {
	root := GinkgoT().TempDir()

	event, _, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"pkg/safe.go"},
		Score:    0.88,
		Reason:   "raw_prompt",
		Verified: true,
		Evidence: LearningEvidenceRef{
			Kind:     "secret_prompt",
			Verified: true,
			Metrics:  map[string]float64{"file_body": 1, "boost_at": 0.75},
		},
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{"pkg/safe.go"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
			VerificationOutput:  "ok",
		},
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonVerifiedFeedback), "sensitive reason should fall back, got %q", event.Reason)

	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	Expect(err).NotTo(HaveOccurred(), "read log: %v", err)

	body := strings.ToLower(string(data))
	for _, marker := range []string{"raw_prompt", "secret_prompt", "file_body", "transcript", "begin openssh"} {
		Expect(body).NotTo(ContainSubstring(marker), "learning log contains privacy marker %q: %s", marker, body)
	}
	Expect(body).To(ContainSubstring(`"kind":"unknown"`), "learning log missing sanitized evidence fields: %s", body)
	Expect(body).To(ContainSubstring(`"boost_at":0.75`), "learning log missing sanitized evidence fields: %s", body)
})
var _ = It("append learning log sanitizes freeform fields", func() {
	root := GinkgoT().TempDir()
	{
		_, err := commitLearningLogFixture(root, LearningLogEntry{
			Timestamp: "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content operator freeform",
			EventID:   "ranker_raw_prompt",
			Source:    "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content operator freeform",
			Action:    "no_op",
			State:     LearningStateAccepted,
			Reason:    "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content operator freeform",
			Files:     []string{"pkg/safe.go", "../escape.go"},
			TaskID:    "raw_prompt",
			CorpusID:  "corpus-1",
			ResultID:  "transcript",
			Evidence: &LearningEvidenceRef{
				Kind:     "secret_prompt",
				Verified: true,
				Files:    []string{"pkg/safe.go", "../escape.go"},
				Metrics:  map[string]float64{"file_body": 1, "safe_metric": 2},
			},
			Note: "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content operator freeform",
		})
		Expect(err).NotTo(HaveOccurred(), "append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	Expect(err).NotTo(HaveOccurred(), "read log: %v", err)

	body := strings.ToLower(string(data))
	for _, marker := range []string{"raw_prompt", "secret_prompt", "file_body", "begin openssh", "transcript", "protected content", "operator freeform", "../escape.go", "/absolute.go"} {
		Expect(body).NotTo(ContainSubstring(marker), "learning log contains unsanitized marker %q: %s", marker, body)
	}
	Expect(body).To(ContainSubstring(`"note":"`+LearningLogNotePresent+`"`), "learning log missing sanitized audit fields: %s", body)
	Expect(body).To(ContainSubstring(`"kind":"unknown"`), "learning log missing sanitized audit fields: %s", body)
	Expect(body).To(ContainSubstring(`"unknown":2`), "learning log missing sanitized audit fields: %s", body)
	Expect(body).To(ContainSubstring(`"pkg/safe.go"`), "learning log missing sanitized audit fields: %s", body)

	entries, err := ReadLearningLog(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "read entries: %v", err)
	Expect(len(entries)).To(BeEquivalentTo(1), "expected one entry, got %#v", entries)

	entry := entries[0]
	Expect(learningGeneratedEventIDPattern.MatchString(entry.EventID)).To(BeTrue(), "freeform fields were not reduced to safe audit values: %#v", entry)
	Expect(entry.Source).To(BeEquivalentTo("unknown"), "freeform fields were not reduced to safe audit values: %#v", entry)
	Expect(entry.Note).To(BeEquivalentTo(LearningLogNotePresent), "freeform fields were not reduced to safe audit values: %#v", entry)
	Expect(entry.Reason).To(BeEquivalentTo("unknown"), "freeform fields were not reduced to safe audit values: %#v", entry)
	Expect(entry.TaskID).To(HavePrefix("task_"), "freeform fields were not reduced to safe audit values: %#v", entry)
	Expect(entry.CorpusID).To(HavePrefix("corpus_"), "freeform fields were not reduced to safe audit values: %#v", entry)
	Expect(entry.ResultID).To(HavePrefix("result_"), "freeform fields were not reduced to safe audit values: %#v", entry)
	{
		_, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		Expect(err).NotTo(HaveOccurred(), "timestamp was not regenerated as parseable RFC3339Nano: %q: %v", entry.Timestamp, err)
	}
	Expect(len(entry.Files)).To(BeEquivalentTo(1), "files were not normalized: %#v", entry.Files)
	Expect(entry.Files[0]).To(BeEquivalentTo("pkg/safe.go"), "files were not normalized: %#v", entry.Files)
	Expect(entry.Evidence).NotTo(BeNil(), "evidence was not sanitized usefully: %#v", entry.Evidence)
	Expect(entry.Evidence.Kind).To(BeEquivalentTo("unknown"), "evidence was not sanitized usefully: %#v", entry.Evidence)
	Expect(entry.Evidence.Metrics["unknown"]).To(BeEquivalentTo(2), "evidence was not sanitized usefully: %#v", entry.Evidence)
})
var _ = It("append learning log strictly bounds token like fields", func() {
	root := GinkgoT().TempDir()
	rawValues := []string{
		"ghp_1234567890abcdef",
		"sk_live_demo_token",
		"normal_token",
	}
	compoundValues := []string{
		"api-key",
		"private-key",
		"access-key",
		"api_key",
		"private.key",
		"access.key",
		"apiKey",
		"privateKey",
		"accessKey",
		"secret-key",
		"token-key",
		"credential-key",
		"credentials-key",
	}
	blockedPathValues := []string{
		rawValues[0] + "/safe.go",
		rawValues[1] + "/pkg.go",
		rawValues[2] + "/file.go",
		"pkg/" + rawValues[0] + ".go",
		"pkg/" + rawValues[1] + ".go",
		"pkg/" + rawValues[2] + ".go",
		"api-key/safe.go",
		"pkg/private-key.go",
		"access-key/safe.go",
		"api_key/safe.go",
		"pkg/private.key.go",
		"access.key/safe.go",
		"pkg/api_key.go",
		"pkg/access.key.go",
		"apiKey/safe.go",
		"pkg/privateKey.go",
		"accessKey/safe.go",
		"pkg/apiKey.go",
		"secret-key/safe.go",
		"token-key/safe.go",
		"credential-key/safe.go",
		"credentials-key/safe.go",
		"pkg/api-key-store.go",
		"pkg/private.key.store.go",
	}
	fileInputs := append([]string{}, rawValues...)

	fileInputs = append(fileInputs, compoundValues...)
	fileInputs = append(fileInputs, blockedPathValues...)
	fileInputs = append(fileInputs, "pkg/safe.go", "internal/repository/comprehension/learning_log.go", "cmd/idiom/ranker.go")

	evidenceFileInputs := append([]string{}, rawValues...)

	evidenceFileInputs = append(evidenceFileInputs, compoundValues...)
	evidenceFileInputs = append(evidenceFileInputs, blockedPathValues...)
	evidenceFileInputs = append(evidenceFileInputs, "internal/runtime/agent/runner.go", "cmd/idiom/ranker.go")

	beforeWeights := map[string]float64{
		"pkg/safe.go": 1.2,
		"internal/repository/comprehension/learning_log.go": 1.25,
		rawValues[0]: 1.3,
		rawValues[1]: 1.4,
	}
	afterWeights := map[string]float64{
		"pkg/safe.go":         1.5,
		"cmd/idiom/ranker.go": 1.55,
		rawValues[1]:          1.6,
		rawValues[2]:          1.7,
	}

	for i, value := range compoundValues {
		beforeWeights[value] = float64(10 + i)
		afterWeights[value] = float64(20 + i)
	}

	for i, path := range blockedPathValues {
		beforeWeights[path] = float64(2 + i)
		afterWeights[path] = float64(3 + i)
	}

	rollbackWeights := map[string]float64{
		"pkg/safe.go": 1.2,
		rawValues[0]:  1.3,
		rawValues[2]:  1.4,
	}
	for i, value := range compoundValues {
		rollbackWeights[value] = float64(30 + i)
	}

	for i, path := range blockedPathValues {
		rollbackWeights[path] = float64(4 + i)
	}
	{
		_, err := commitLearningLogFixture(root, LearningLogEntry{
			Timestamp: "2026-05-13T10:11:12Z",
			EventID:   rawValues[2],
			Source:    rawValues[0],
			Action:    "no_op",
			State:     LearningStateAccepted,
			Reason:    rawValues[1],
			Files:     fileInputs,
			TaskID:    rawValues[0],
			CorpusID:  rawValues[1],
			ResultID:  rawValues[2],
			Evidence: &LearningEvidenceRef{
				Kind:     rawValues[0],
				Verified: true,
				TaskID:   rawValues[1],
				CorpusID: rawValues[2],
				ResultID: rawValues[0],
				Files:    evidenceFileInputs,
				Metrics: map[string]float64{
					rawValues[0]: 1,
					rawValues[1]: 2,
					rawValues[2]: 3,
					"score":      0.9,
				},
			},
		})
		Expect(err).NotTo(HaveOccurred(), "append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	Expect(err).NotTo(HaveOccurred(), "read log: %v", err)

	body := strings.ToLower(string(data))
	for _, raw := range append(append([]string{}, rawValues...), compoundValues...) {
		Expect(body).NotTo(ContainSubstring(strings.ToLower(raw)), "learning log leaked token-like raw value %q: %s", raw, body)
	}

	for _, raw := range blockedPathValues {
		Expect(body).NotTo(ContainSubstring(strings.ToLower(raw)), "learning log leaked token-like path value %q: %s", raw, body)
	}

	entries, err := ReadLearningLog(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "read entries: %v", err)
	Expect(len(entries)).To(BeEquivalentTo(1), "expected one entry, got %#v", entries)

	entry := entries[0]
	Expect(learningGeneratedEventIDPattern.MatchString(entry.EventID)).To(BeTrue(), "invalid event id was not regenerated: %#v", entry)
	Expect(entry.Source).To(BeEquivalentTo("unknown"), "source/reason should collapse to unknown: %#v", entry)
	Expect(entry.Reason).To(BeEquivalentTo("unknown"), "source/reason should collapse to unknown: %#v", entry)

	wantFiles := []string{"cmd/idiom/ranker.go", "internal/repository/comprehension/learning_log.go", "pkg/safe.go"}
	Expect(strings.Join(entry.Files, ",")).To(BeEquivalentTo(strings.Join(wantFiles, ",")), "path fields should keep bounded paths and drop token-like values: %#v", entry.Files)

	for label, id := range map[string]string{
		"task":   entry.TaskID,
		"corpus": entry.CorpusID,
		"result": entry.ResultID,
	} {
		Expect(learningHashedIDPatterns[label].MatchString(id)).To(BeTrue(), "%s id was not a bounded hash: %#v", label, entry)
	}
	Expect(entry.Evidence).NotTo(BeNil(), "evidence missing: %#v", entry)
	Expect(entry.Evidence.Kind).To(BeEquivalentTo("unknown"), "evidence tokens were not bounded: %#v", entry.Evidence)
	Expect(learningHashedIDPatterns["task"].MatchString(entry.Evidence.TaskID)).To(BeTrue(), "evidence tokens were not bounded: %#v", entry.Evidence)
	Expect(learningHashedIDPatterns["corpus"].MatchString(entry.Evidence.CorpusID)).To(BeTrue(), "evidence tokens were not bounded: %#v", entry.Evidence)
	Expect(learningHashedIDPatterns["result"].MatchString(entry.Evidence.ResultID)).To(BeTrue(), "evidence tokens were not bounded: %#v", entry.Evidence)

	wantEvidenceFiles := []string{"cmd/idiom/ranker.go", "internal/runtime/agent/runner.go"}
	Expect(strings.Join(entry.Evidence.Files, ",")).To(BeEquivalentTo(strings.Join(wantEvidenceFiles, ",")), "evidence files should keep bounded paths and drop token-like values: %#v", entry.Evidence.Files)
	Expect(entry.Evidence.Metrics["score"]).To(BeEquivalentTo(0.9), "metrics should preserve known keys and count unknown keys: %#v", entry.Evidence.Metrics)
	Expect(entry.Evidence.Metrics["unknown"]).To(BeEquivalentTo(3), "metrics should preserve known keys and count unknown keys: %#v", entry.Evidence.Metrics)

	assertLearningLogWeights := func(label string, weights map[string]float64, want map[string]float64) {
		GinkgoHelper()
		Expect(weights).To(HaveLen(len(want)), "%s should keep bounded path weights and drop token-like keys: %#v", label, weights)

		for key, value := range want {
			Expect(weights).To(HaveKeyWithValue(key, value), "%s should keep %s=%v and drop token-like keys: %#v", label, key, value, weights)
		}
	}
	assertLearningLogWeights("before weights", sanitizeLearningLogWeights(beforeWeights), map[string]float64{
		"internal/repository/comprehension/learning_log.go": 1.25,
		"pkg/safe.go": 1.2,
	})
	assertLearningLogWeights("after weights", sanitizeLearningLogWeights(afterWeights), map[string]float64{
		"cmd/idiom/ranker.go": 1.55,
		"pkg/safe.go":         1.5,
	})
	assertLearningLogWeights("rollback weights", sanitizeLearningLogWeights(rollbackWeights), map[string]float64{
		"pkg/safe.go": 1.2,
	})
})
var _ = It("append learning log normalizes valid timestamp", func() {
	root := GinkgoT().TempDir()
	{
		_, err := commitLearningLogFixture(root, LearningLogEntry{
			Timestamp: "2026-05-13T10:11:12-04:00",
			Source:    "cli",
			Action:    "no_op",
			State:     LearningStateAccepted,
		})
		Expect(err).NotTo(HaveOccurred(), "append: %v", err)
	}

	entries, err := ReadLearningLog(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "read entries: %v", err)
	Expect(len(entries)).To(BeEquivalentTo(1), "expected one entry, got %#v", entries)
	Expect(entries[0].Timestamp).To(BeEquivalentTo("2026-05-13T14:11:12Z"), "timestamp was not normalized to UTC RFC3339Nano, got %q", entries[0].Timestamp)
	{
		_, err := time.Parse(time.RFC3339Nano, entries[0].Timestamp)
		Expect(err).NotTo(HaveOccurred(), "normalized timestamp is not parseable: %v", err)
	}
})
var _ = It("apply verified ranker learning honors disable env", func() {
	GinkgoT().Setenv(RankerLearningEnv, "0")

	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"internal/runtime/agent/runner.go"},
		Score:    0.95,
		Verified: true,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{"internal/runtime/agent/runner.go"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
		},
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.State).To(BeEquivalentTo(LearningStateRejected), "event state/reason = %q/%q", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonDisabled), "event state/reason = %q/%q", event.State, event.Reason)
	Expect(len(weights)).To(BeEquivalentTo(0), "disabled learning must not update weights: %v", weights)
})
var _ = It("record verified ranker evidence does not mutate weights", func() {
	root := GinkgoT().TempDir()

	event, err := RecordVerifiedRankerEvidence(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   LearningSourceRecoveryReconciliation,
		Action:   "boost",
		Files:    []string{"internal/workflows/recovery/packet.go"},
		Score:    1,
		TaskID:   "failure-123",
		CorpusID: "packet-123",
		ResultID: "reconciliation-123",
		Reason:   LearningReasonVerifiedRecovery,
		Verified: true,
		Evidence: LearningEvidenceRef{
			Kind:     LearningEvidenceKindRecoveryReconciliation,
			Verified: true,
			TaskID:   "failure-123",
			CorpusID: "packet-123",
			ResultID: "reconciliation-123",
			Files:    []string{"internal/workflows/recovery/packet.go"},
			Metrics: map[string]float64{
				"candidate_atom_count":       2,
				"completed_atom_count":       1,
				"remaining_atom_count":       1,
				"required_test_count":        1,
				"verification_command_count": 1,
				"localization_improved":      1,
				"packet_quality":             0.5,
			},
		},
	})
	Expect(err).NotTo(HaveOccurred(), "record evidence: %v", err)
	Expect(event.Source).To(BeEquivalentTo(LearningSourceRecoveryReconciliation), "unexpected evidence event: %#v", event)
	Expect(event.Action).To(BeEquivalentTo("no_op"), "unexpected evidence event: %#v", event)
	Expect(event.State).To(BeEquivalentTo(LearningStateAccepted), "unexpected evidence event: %#v", event)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonVerifiedRecovery), "unexpected evidence event: %#v", event)
	Expect(event.Evidence).NotTo(BeNil(), "recovery evidence metrics were not preserved: %#v", event.Evidence)
	Expect(event.Evidence.Kind).To(BeEquivalentTo(LearningEvidenceKindRecoveryReconciliation), "recovery evidence metrics were not preserved: %#v", event.Evidence)
	Expect(event.Evidence.Metrics["packet_quality"]).To(BeEquivalentTo(0.5), "recovery evidence metrics were not preserved: %#v", event.Evidence)
	Expect(event.Evidence.Metrics["completed_atom_count"]).To(BeEquivalentTo(1), "recovery evidence metrics were not preserved: %#v", event.Evidence)

	weights, err := LoadLearnedWeights(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "load weights: %v", err)
	Expect(len(weights)).To(BeEquivalentTo(0), "evidence-only event must not update weights: %v", weights)
	{
		_, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "weights.json"))
		Expect(os.IsNotExist(err)).To(BeTrue(), "evidence-only event must not create weights.json, err=%v", err)
	}

	entries, err := ReadLearningLog(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "read learning log: %v", err)

	summary := SummarizeLearningLog(entries)
	Expect(summary.BySource[LearningSourceRecoveryReconciliation]).To(BeEquivalentTo(1), "summary missing recovery evidence: %#v", summary)
	Expect(summary.AcceptedReasonCounts[LearningReasonVerifiedRecovery]).To(BeEquivalentTo(1), "summary missing recovery evidence: %#v", summary)
})
var _ = It("restore learned weights from learning log", func() {
	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"internal/runtime/agent/runner.go"},
		Score:    0.9,
		Verified: true,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{"internal/runtime/agent/runner.go"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
		},
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(weights["internal/runtime/agent/runner.go"] <= LearnedWeightBaseline).To(BeFalse(), "expected boosted weight before rollback, got %v", weights)

	restored, restoreEvent, err := RestoreLearnedWeightsFromLearningLog(GinkgoT().Context(), root, event.EventID, "restore-test")
	Expect(err).NotTo(HaveOccurred(), "restore: %v", err)
	Expect(len(restored)).To(BeEquivalentTo(0), "rollback should restore empty baseline weights, got %v", restored)
	Expect(restoreEvent.Action).To(BeEquivalentTo("restore"), "restore event missing rollback metadata: %#v", restoreEvent)
	Expect(restoreEvent.State).To(BeEquivalentTo(LearningStateAccepted), "restore event missing rollback metadata: %#v", restoreEvent)
	Expect(restoreEvent.ResultID).To(BeEquivalentTo(event.EventID), "restore event missing rollback metadata: %#v", restoreEvent)

	entries, _ := ReadLearningLog(GinkgoT().Context(), root)
	summary := SummarizeLearningLog(entries)
	Expect(summary.RestoreCount).To(BeEquivalentTo(1), "summary missing restore metrics: %#v", summary)
	Expect(summary.AcceptedReasonCounts[LearningReasonRollbackRestore]).To(BeEquivalentTo(1), "summary missing restore metrics: %#v", summary)
})
var _ = It("apply verified ranker learning rejects missing verification evidence", func() {
	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"internal/runtime/agent/runner.go"},
		Score:    0.9,
		Verified: true,
		// VerificationEvidence intentionally nil
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.State).To(BeEquivalentTo(LearningStateRejected), "event state/reason = %q/%q, want rejected/verification_evidence_missing", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonVerificationEvidenceMissing), "event state/reason = %q/%q, want rejected/verification_evidence_missing", event.State, event.Reason)
	Expect(len(weights)).To(BeEquivalentTo(0), "missing verification evidence must not update weights: %v", weights)
})
var _ = It("apply verified ranker learning rejects empty edit paths", func() {
	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"internal/runtime/agent/runner.go"},
		Score:    0.9,
		Verified: true,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{},
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
		},
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.State).To(BeEquivalentTo(LearningStateRejected), "event state/reason = %q/%q, want rejected/no_edit_paths", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonNoEditPaths), "event state/reason = %q/%q, want rejected/no_edit_paths", event.State, event.Reason)
	Expect(len(weights)).To(BeEquivalentTo(0), "empty edit paths must not update weights: %v", weights)
})
var _ = It("apply verified ranker learning rejects verification not passed", func() {
	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"internal/runtime/agent/runner.go"},
		Score:    0.9,
		Verified: true,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{"internal/runtime/agent/runner.go"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  false,
			VerificationOutput:  "FAIL",
		},
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.State).To(BeEquivalentTo(LearningStateRejected), "event state/reason = %q/%q, want rejected/verification_not_passed", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonVerificationNotPassed), "event state/reason = %q/%q, want rejected/verification_not_passed", event.State, event.Reason)
	Expect(len(weights)).To(BeEquivalentTo(0), "failed verification must not update weights: %v", weights)
})
var _ = It("apply verified ranker learning rejects protected edit paths", func() {
	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{".idiom/ranker/weights.json"},
		Score:    0.9,
		Verified: true,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{".idiom/ranker/weights.json"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
		},
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.State).To(BeEquivalentTo(LearningStateRejected), "event state/reason = %q/%q, want rejected/protected_path_in_edits", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonProtectedPathInEdits), "event state/reason = %q/%q, want rejected/protected_path_in_edits", event.State, event.Reason)
	Expect(len(weights)).To(BeEquivalentTo(0), "protected edit paths must not update weights: %v", weights)
})
var _ = It("apply verified ranker learning rejects edit path mismatch", func() {
	root := GinkgoT().TempDir()

	event, weights, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"pkg/other.go"},
		Score:    0.9,
		Verified: true,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{"internal/runtime/agent/runner.go"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
		},
	})
	Expect(err).NotTo(HaveOccurred(), "apply: %v", err)
	Expect(event.State).To(BeEquivalentTo(LearningStateRejected), "event state/reason = %q/%q, want rejected/edit_path_mismatch", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonEditPathMismatch), "event state/reason = %q/%q, want rejected/edit_path_mismatch", event.State, event.Reason)
	Expect(len(weights)).To(BeEquivalentTo(0), "mismatched edit paths must not update weights: %v", weights)
})
var _ = It("record verified ranker evidence unaffected by verification gate", func() {
	root := GinkgoT().TempDir()
	// RecordVerifiedRankerEvidence does NOT require VerificationEvidence.
	event, err := RecordVerifiedRankerEvidence(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   LearningSourceRecoveryReconciliation,
		Action:   "no_op",
		Files:    []string{"internal/workflows/recovery/packet.go"},
		Score:    1,
		TaskID:   "failure-123",
		CorpusID: "packet-123",
		ResultID: "reconciliation-123",
		Reason:   LearningReasonVerifiedRecovery,
		Verified: true,
		Evidence: LearningEvidenceRef{
			Kind:     LearningEvidenceKindRecoveryReconciliation,
			Verified: true,
		},
		// No VerificationEvidence — recovery reconciliation bypasses the gate.
	})
	Expect(err).NotTo(HaveOccurred(), "record evidence: %v", err)
	Expect(event.State).To(BeEquivalentTo(LearningStateAccepted), "recovery evidence should be accepted without verification gate, got state=%q reason=%q", event.State, event.Reason)
	Expect(event.Reason).To(BeEquivalentTo(LearningReasonVerifiedRecovery), "recovery evidence should be accepted without verification gate, got state=%q reason=%q", event.State, event.Reason)
})

// TestHasProtectedEditPath covers protected-path gate edge cases including the
// R1 (false positive on .idiom prefix substring) and R2 (./ bypass) fixes.
var _ = DescribeTable("has protected edit path",
	func(paths []string, expected bool) {
		Expect(hasProtectedEditPath(paths)).To(Equal(expected))
	},
	Entry("protects the .idiom directory", []string{".idiom/ranker/weights.json"}, true),
	Entry("protects the bare .idiom path", []string{".idiom"}, true),
	Entry("protects deep .idiom paths", []string{".idiom/ranker/learning_log.jsonl"}, true),
	Entry("does not treat .idiom_backup as .idiom", []string{".idiom_backup/foo.go"}, false),
	Entry("does not treat .idiom_config as .idiom", []string{".idiom_config/config.json"}, false),
	Entry("does not treat .idiom-temp as .idiom", []string{".idiom-temp/data.txt"}, false),
	Entry("rejects a dot-prefixed .idiom directory bypass", []string{"./.idiom/ranker/weights.json"}, true),
	Entry("rejects a dot-prefixed bare .idiom bypass", []string{"./.idiom"}, true),
	Entry("rejects a deep dot-prefixed .idiom bypass", []string{"./.idiom/comprehension/graph.sqlite"}, true),
	Entry("allows normal Go source", []string{"internal/runtime/agent/runner.go"}, false),
	Entry("allows multiple normal paths", []string{"pkg/safe.go", "cmd/idiom/main.go"}, false),
	Entry("rejects a parent-directory escape", []string{"../escape.go"}, true),
	Entry("rejects an absolute path", []string{"/etc/passwd"}, true),
	Entry("allows an empty path", []string{""}, false),
	Entry("allows a whitespace-only path", []string{"   "}, false),
	Entry("rejects a protected path mixed with a normal path", []string{"pkg/safe.go", ".idiom/ranker/weights.json"}, true),
	Entry("allows an .idiom_backup path mixed with a normal path", []string{"pkg/safe.go", ".idiom_backup/foo.go"}, false),
)

// TestApplyVerifiedRankerLearningStoresVerificationEvidenceInLog verifies that
// the VerificationEvidence gate predicates (EditPaths, VerificationCommand,
// VerificationPassed) are persisted in the learning log entry so gate decisions
// are reconstructable from the audit trail. VerificationOutput is intentionally
// excluded from the log for privacy.
var _ = It("apply verified ranker learning stores verification evidence in log", func() {
	root := GinkgoT().TempDir()
	GinkgoT().Setenv(RankerLearningEnv, "1")

	editPaths := []string{"pkg/target.go", "pkg/target_test.go"}

	entry, _, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    editPaths,
		Score:    0.85,
		TaskID:   "task-verify-log",
		CorpusID: "corpus-verify-log",
		Verified: true,
		Reason:   LearningReasonVerifiedFeedback,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           editPaths,
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
			VerificationOutput:  "ok  pkg  (cached)", // should be excluded from log
		},
	})
	Expect(err).NotTo(HaveOccurred(), "ApplyVerifiedRankerLearning: %v", err)
	Expect(entry.State).To(BeEquivalentTo(LearningStateAccepted), "expected accepted, got %q", entry.State)
	Expect(

		// Verification evidence must be present in the log entry.
		entry.Verification).NotTo(BeNil(), "entry.Verification should not be nil")
	Expect(entry.Verification.VerificationPassed).To(BeTrue(), "entry.Verification.VerificationPassed should be true")
	Expect(entry.Verification.VerificationCommand).To(BeEquivalentTo("go test ./..."), "entry.Verification.VerificationCommand = %q, want %q", entry.Verification.VerificationCommand, "go test ./...")
	Expect(len(entry.Verification.EditPaths)).To(BeEquivalentTo(2), "entry.Verification.EditPaths = %v, want 2 paths", entry.Verification.EditPaths)

	// Read back from the learning log and verify the persisted entry.
	entries, err := ReadLearningLog(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadLearningLog: %v", err)
	Expect(len(entries)).NotTo(BeEquivalentTo(0), "learning log should have at least one entry")

	persisted := entries[0]
	Expect(persisted.Verification).NotTo(BeNil(), "persisted entry.Verification should not be nil")
	Expect(persisted.Verification.VerificationPassed).To(BeTrue(), "persisted VerificationPassed should be true")
	Expect(len(persisted.Verification.EditPaths)).To(BeEquivalentTo(2), "persisted EditPaths = %v, want 2 paths", persisted.Verification.EditPaths)
	Expect(persisted.Verification.VerificationCommand).To(BeEquivalentTo("go test ./..."), "persisted VerificationCommand = %q", persisted.Verification.VerificationCommand)
})

// TestApplyVerifiedRankerLearningLogExcludesVerificationOutput verifies that
// VerificationOutput is NOT stored in the learning log's verification evidence.
// The log carries structured gate predicates only; raw command output stays out.
var _ = It("apply verified ranker learning log excludes verification output", func() {
	root := GinkgoT().TempDir()
	GinkgoT().Setenv(RankerLearningEnv, "1")

	_, _, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"pkg/target.go"},
		Score:    0.85,
		TaskID:   "task-no-output",
		CorpusID: "corpus-no-output",
		Verified: true,
		Reason:   LearningReasonVerifiedFeedback,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{"pkg/target.go"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  true,
			VerificationOutput:  "some raw command output that should NOT appear in the log",
		},
	})
	Expect(err).NotTo(HaveOccurred(), "ApplyVerifiedRankerLearning: %v", err)

	// Read back from the log.
	entries, err := ReadLearningLog(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadLearningLog: %v", err)
	Expect(len(entries)).NotTo(BeEquivalentTo(0), "learning log should have at least one entry")

	persisted := entries[0]
	Expect(persisted.Verification).NotTo(BeNil(), "persisted entry.Verification should not be nil")

	// The LearningLogVerificationEvidence type has no VerificationOutput field,
	// so this is a structural guarantee. The raw output must not leak into any
	// persisted field.
	raw, err := json.Marshal(persisted)
	Expect(err).NotTo(HaveOccurred(), "json.Marshal: %v", err)
	Expect(string(raw)).NotTo(ContainSubstring("some raw command output"), "raw VerificationOutput should not appear in persisted learning log entry")
	Expect(string(raw)).NotTo(ContainSubstring("verification_output"), "verification_output field should not appear in persisted learning log entry")
})

// TestApplyVerifiedRankerLearningRejectedStoresVerificationEvidence verifies that
// even when the gate rejects a request, the verification evidence is still
// persisted so the rejection reason can be correlated with the evidence that
// triggered it.
var _ = It("apply verified ranker learning rejected stores verification evidence", func() {
	root := GinkgoT().TempDir()
	GinkgoT().Setenv(RankerLearningEnv, "1")

	// A request where verification did NOT pass — should be rejected.
	entry, _, err := ApplyVerifiedRankerLearning(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"pkg/target.go"},
		Score:    0.85,
		TaskID:   "task-rejected",
		Verified: true,
		Reason:   LearningReasonVerifiedFeedback,
		VerificationEvidence: &VerificationEvidence{
			EditPaths:           []string{"pkg/target.go"},
			VerificationCommand: "go test ./...",
			VerificationPassed:  false, // verification failed
			VerificationOutput:  "FAIL",
		},
	})
	Expect(err).NotTo(HaveOccurred(), "ApplyVerifiedRankerLearning: %v", err)
	Expect(entry.State).To(BeEquivalentTo(LearningStateRejected), "expected rejected, got %q", entry.State)
	Expect(entry.Reason).To(BeEquivalentTo(LearningReasonVerificationNotPassed), "expected reason verification_not_passed, got %q", entry.Reason)
	Expect(

		// Verification evidence must still be logged so the audit trail shows what failed.
		entry.Verification).NotTo(BeNil(), "entry.Verification should not be nil for rejected entries")
	Expect(entry.Verification.VerificationPassed).To(BeFalse(), "entry.Verification.VerificationPassed should be false")
	Expect(len(entry.Verification.EditPaths)).To(BeEquivalentTo(1), "entry.Verification.EditPaths = %v, want 1 path", entry.Verification.EditPaths)

	// Verify from persisted log.
	entries, err := ReadLearningLog(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadLearningLog: %v", err)
	Expect(len(entries)).NotTo(BeEquivalentTo(0), "learning log should have at least one entry")

	persisted := entries[0]
	Expect(persisted.Verification).NotTo(BeNil(), "persisted entry.Verification should not be nil for rejected entries")
	Expect(persisted.Verification.VerificationPassed).To(BeFalse(), "persisted VerificationPassed should be false")
	Expect(persisted.Verification.VerificationCommand).To(BeEquivalentTo("go test ./..."), "persisted VerificationCommand = %q", persisted.Verification.VerificationCommand)
})

func commitLearningLogFixture(root string, entry LearningLogEntry) (LearningLogEntry, error) {
	store, err := openRankerStateStore(context.Background(), root)
	if err != nil {
		return LearningLogEntry{}, err
	}
	defer store.Close()
	if err := store.recoverLocked(); err != nil {
		return LearningLogEntry{}, err
	}
	weights, err := store.loadWeightsLocked()
	if err != nil {
		return LearningLogEntry{}, err
	}

	committed, _, err := store.commitLocked(rankerStateChange{
		entry:  entry,
		before: weights,
		after:  weights,
	})

	return committed, err
}
