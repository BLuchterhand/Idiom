package comprehension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyVerifiedRankerLearningAcceptsVerifiedEvidence(t *testing.T) {
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.EventID == "" {
		t.Fatalf("event id missing: %#v", event)
	}
	if event.State != LearningStateAccepted || event.Reason != "swe_groundedness_oracle" {
		t.Fatalf("event state/reason = %q/%q", event.State, event.Reason)
	}
	if got := weights["internal/runtime/agent/runner.go"]; got <= LearnedWeightBaseline {
		t.Fatalf("expected boosted weight, got %v", got)
	}
	if _, ok := weights["../escape.go"]; ok {
		t.Fatalf("invalid path should not be weighted: %v", weights)
	}
	if event.Evidence == nil || event.Evidence.InvalidPathCount != 1 || len(event.Evidence.Files) != 1 {
		t.Fatalf("evidence should carry sanitized path details: %#v", event.Evidence)
	}
	if event.BeforeWeights == nil || event.AfterWeights == nil || event.Rollback == nil {
		t.Fatalf("event missing rollback data: %#v", event)
	}
	entries, err := ReadLearningLog(root)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(entries) != 1 || entries[0].EventID != event.EventID {
		t.Fatalf("unexpected log entries: %#v", entries)
	}
	summary := SummarizeLearningLog(entries)
	if summary.TotalEvents != 1 || summary.AcceptedCount != 1 || summary.AcceptedReasonCounts["swe_groundedness_oracle"] != 1 {
		t.Fatalf("summary missing accepted event counts: %#v", summary)
	}
}

func TestApplyVerifiedRankerLearningRejectsUnverifiedEvidence(t *testing.T) {
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
		Source: "eval",
		Action: "boost",
		Files:  []string{"internal/runtime/agent/runner.go"},
		Score:  0.9,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.State != LearningStateRejected || event.Reason != LearningReasonUnverified {
		t.Fatalf("event state/reason = %q/%q", event.State, event.Reason)
	}
	if len(weights) != 0 {
		t.Fatalf("unverified evidence must not update weights: %v", weights)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "weights.json")); !os.IsNotExist(err) {
		t.Fatalf("unverified evidence should not create weights.json, err=%v", err)
	}
	entries, _ := ReadLearningLog(root)
	summary := SummarizeLearningLog(entries)
	if summary.RejectedCount != 1 || summary.RejectedReasonCounts[LearningReasonUnverified] != 1 {
		t.Fatalf("summary missing rejected reason counts: %#v", summary)
	}
}

func TestApplyVerifiedRankerLearningLogIsPrivacySafe(t *testing.T) {
	root := t.TempDir()
	event, _, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.Reason != LearningReasonVerifiedFeedback {
		t.Fatalf("sensitive reason should fall back, got %q", event.Reason)
	}
	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	body := strings.ToLower(string(data))
	for _, marker := range []string{"raw_prompt", "secret_prompt", "file_body", "transcript", "begin openssh"} {
		if strings.Contains(body, marker) {
			t.Fatalf("learning log contains privacy marker %q: %s", marker, body)
		}
	}
	if !strings.Contains(body, `"kind":"unknown"`) || !strings.Contains(body, `"boost_at":0.75`) {
		t.Fatalf("learning log missing sanitized evidence fields: %s", body)
	}
}

func TestAppendLearningLogSanitizesFreeformFields(t *testing.T) {
	root := t.TempDir()
	if err := AppendLearningLog(root, LearningLogEntry{
		Timestamp: "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content operator freeform",
		EventID:   "ranker_raw_prompt",
		Source:    "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content operator freeform",
		Action:    "boost",
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
		BeforeWeights: &map[string]float64{
			"pkg/safe.go":  1.3,
			"../escape.go": 2,
			"/absolute.go": 2,
		},
		AfterWeights: &map[string]float64{
			"pkg/safe.go": 1.4,
		},
		Rollback: &LearningRollbackData{Weights: map[string]float64{
			"pkg/safe.go":  1.3,
			"/absolute.go": 2,
		}},
		Note: "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content operator freeform",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	body := strings.ToLower(string(data))
	for _, marker := range []string{"raw_prompt", "secret_prompt", "file_body", "begin openssh", "transcript", "protected content", "operator freeform", "../escape.go", "/absolute.go"} {
		if strings.Contains(body, marker) {
			t.Fatalf("learning log contains unsanitized marker %q: %s", marker, body)
		}
	}
	if !strings.Contains(body, `"note":"`+LearningLogNotePresent+`"`) ||
		!strings.Contains(body, `"kind":"unknown"`) ||
		!strings.Contains(body, `"unknown":2`) ||
		!strings.Contains(body, `"pkg/safe.go"`) {
		t.Fatalf("learning log missing sanitized audit fields: %s", body)
	}

	entries, err := ReadLearningLog(root)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %#v", entries)
	}
	entry := entries[0]
	if !learningGeneratedEventIDPattern.MatchString(entry.EventID) ||
		entry.Source != "unknown" ||
		entry.Note != LearningLogNotePresent ||
		entry.Reason != "unknown" ||
		!strings.HasPrefix(entry.TaskID, "task_") ||
		!strings.HasPrefix(entry.CorpusID, "corpus_") ||
		!strings.HasPrefix(entry.ResultID, "result_") {
		t.Fatalf("freeform fields were not reduced to safe audit values: %#v", entry)
	}
	if _, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err != nil {
		t.Fatalf("timestamp was not regenerated as parseable RFC3339Nano: %q: %v", entry.Timestamp, err)
	}
	if len(entry.Files) != 1 || entry.Files[0] != "pkg/safe.go" {
		t.Fatalf("files were not normalized: %#v", entry.Files)
	}
	if entry.Evidence == nil || entry.Evidence.Kind != "unknown" || entry.Evidence.Metrics["unknown"] != 2 {
		t.Fatalf("evidence was not sanitized usefully: %#v", entry.Evidence)
	}
}

func TestAppendLearningLogStrictlyBoundsTokenLikeFields(t *testing.T) {
	root := t.TempDir()
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
	if err := AppendLearningLog(root, LearningLogEntry{
		Timestamp: "2026-05-13T10:11:12Z",
		EventID:   rawValues[2],
		Source:    rawValues[0],
		Action:    "boost",
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
		BeforeWeights: &beforeWeights,
		AfterWeights:  &afterWeights,
		Rollback:      &LearningRollbackData{Weights: rollbackWeights},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	body := strings.ToLower(string(data))
	for _, raw := range append(append([]string{}, rawValues...), compoundValues...) {
		if strings.Contains(body, strings.ToLower(raw)) {
			t.Fatalf("learning log leaked token-like raw value %q: %s", raw, body)
		}
	}
	for _, raw := range blockedPathValues {
		if strings.Contains(body, strings.ToLower(raw)) {
			t.Fatalf("learning log leaked token-like path value %q: %s", raw, body)
		}
	}

	entries, err := ReadLearningLog(root)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %#v", entries)
	}
	entry := entries[0]
	if !learningGeneratedEventIDPattern.MatchString(entry.EventID) {
		t.Fatalf("invalid event id was not regenerated: %#v", entry)
	}
	if entry.Source != "unknown" || entry.Reason != "unknown" {
		t.Fatalf("source/reason should collapse to unknown: %#v", entry)
	}
	wantFiles := []string{"cmd/idiom/ranker.go", "internal/repository/comprehension/learning_log.go", "pkg/safe.go"}
	if strings.Join(entry.Files, ",") != strings.Join(wantFiles, ",") {
		t.Fatalf("path fields should keep bounded paths and drop token-like values: %#v", entry.Files)
	}
	for label, id := range map[string]string{
		"task":   entry.TaskID,
		"corpus": entry.CorpusID,
		"result": entry.ResultID,
	} {
		if !learningHashedIDPatterns[label].MatchString(id) {
			t.Fatalf("%s id was not a bounded hash: %#v", label, entry)
		}
	}
	if entry.Evidence == nil {
		t.Fatalf("evidence missing: %#v", entry)
	}
	if entry.Evidence.Kind != "unknown" ||
		!learningHashedIDPatterns["task"].MatchString(entry.Evidence.TaskID) ||
		!learningHashedIDPatterns["corpus"].MatchString(entry.Evidence.CorpusID) ||
		!learningHashedIDPatterns["result"].MatchString(entry.Evidence.ResultID) {
		t.Fatalf("evidence tokens were not bounded: %#v", entry.Evidence)
	}
	wantEvidenceFiles := []string{"cmd/idiom/ranker.go", "internal/runtime/agent/runner.go"}
	if strings.Join(entry.Evidence.Files, ",") != strings.Join(wantEvidenceFiles, ",") {
		t.Fatalf("evidence files should keep bounded paths and drop token-like values: %#v", entry.Evidence.Files)
	}
	if entry.Evidence.Metrics["score"] != 0.9 || entry.Evidence.Metrics["unknown"] != 3 {
		t.Fatalf("metrics should preserve known keys and count unknown keys: %#v", entry.Evidence.Metrics)
	}
	assertLearningLogWeights := func(label string, weights map[string]float64, want map[string]float64) {
		t.Helper()
		if len(weights) != len(want) {
			t.Fatalf("%s should keep bounded path weights and drop token-like keys: %#v", label, weights)
		}
		for key, value := range want {
			if weights[key] != value {
				t.Fatalf("%s should keep %s=%v and drop token-like keys: %#v", label, key, value, weights)
			}
		}
	}
	if entry.BeforeWeights == nil || entry.AfterWeights == nil || entry.Rollback == nil {
		t.Fatalf("weight snapshots missing: %#v", entry)
	}
	assertLearningLogWeights("before weights", *entry.BeforeWeights, map[string]float64{
		"internal/repository/comprehension/learning_log.go": 1.25,
		"pkg/safe.go": 1.2,
	})
	assertLearningLogWeights("after weights", *entry.AfterWeights, map[string]float64{
		"cmd/idiom/ranker.go": 1.55,
		"pkg/safe.go":         1.5,
	})
	assertLearningLogWeights("rollback weights", entry.Rollback.Weights, map[string]float64{
		"pkg/safe.go": 1.2,
	})
}

func TestAppendLearningLogNormalizesValidTimestamp(t *testing.T) {
	root := t.TempDir()
	if err := AppendLearningLog(root, LearningLogEntry{
		Timestamp: "2026-05-13T10:11:12-04:00",
		Source:    "cli",
		Action:    "reset",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	entries, err := ReadLearningLog(root)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %#v", entries)
	}
	if entries[0].Timestamp != "2026-05-13T14:11:12Z" {
		t.Fatalf("timestamp was not normalized to UTC RFC3339Nano, got %q", entries[0].Timestamp)
	}
	if _, err := time.Parse(time.RFC3339Nano, entries[0].Timestamp); err != nil {
		t.Fatalf("normalized timestamp is not parseable: %v", err)
	}
}

func TestApplyVerifiedRankerLearningHonorsDisableEnv(t *testing.T) {
	t.Setenv(RankerLearningEnv, "0")
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.State != LearningStateRejected || event.Reason != LearningReasonDisabled {
		t.Fatalf("event state/reason = %q/%q", event.State, event.Reason)
	}
	if len(weights) != 0 {
		t.Fatalf("disabled learning must not update weights: %v", weights)
	}
}

func TestRecordVerifiedRankerEvidenceDoesNotMutateWeights(t *testing.T) {
	root := t.TempDir()
	event, err := RecordVerifiedRankerEvidence(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("record evidence: %v", err)
	}
	if event.Source != LearningSourceRecoveryReconciliation || event.Action != "no_op" ||
		event.State != LearningStateAccepted || event.Reason != LearningReasonVerifiedRecovery {
		t.Fatalf("unexpected evidence event: %#v", event)
	}
	if event.Evidence == nil || event.Evidence.Kind != LearningEvidenceKindRecoveryReconciliation ||
		event.Evidence.Metrics["packet_quality"] != 0.5 ||
		event.Evidence.Metrics["completed_atom_count"] != 1 {
		t.Fatalf("recovery evidence metrics were not preserved: %#v", event.Evidence)
	}
	weights, err := LoadLearnedWeights(root)
	if err != nil {
		t.Fatalf("load weights: %v", err)
	}
	if len(weights) != 0 {
		t.Fatalf("evidence-only event must not update weights: %v", weights)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "weights.json")); !os.IsNotExist(err) {
		t.Fatalf("evidence-only event must not create weights.json, err=%v", err)
	}
	entries, err := ReadLearningLog(root)
	if err != nil {
		t.Fatalf("read learning log: %v", err)
	}
	summary := SummarizeLearningLog(entries)
	if summary.BySource[LearningSourceRecoveryReconciliation] != 1 || summary.AcceptedReasonCounts[LearningReasonVerifiedRecovery] != 1 {
		t.Fatalf("summary missing recovery evidence: %#v", summary)
	}
}

func TestRestoreLearnedWeightsFromLearningLog(t *testing.T) {
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if weights["internal/runtime/agent/runner.go"] <= LearnedWeightBaseline {
		t.Fatalf("expected boosted weight before rollback, got %v", weights)
	}
	restored, restoreEvent, err := RestoreLearnedWeightsFromLearningLog(root, event.EventID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(restored) != 0 {
		t.Fatalf("rollback should restore empty baseline weights, got %v", restored)
	}
	if restoreEvent.Action != "restore" || restoreEvent.State != LearningStateAccepted || restoreEvent.ResultID != event.EventID {
		t.Fatalf("restore event missing rollback metadata: %#v", restoreEvent)
	}
	entries, _ := ReadLearningLog(root)
	summary := SummarizeLearningLog(entries)
	if summary.RestoreCount != 1 || summary.AcceptedReasonCounts[LearningReasonRollbackRestore] != 1 {
		t.Fatalf("summary missing restore metrics: %#v", summary)
	}
}

func TestApplyVerifiedRankerLearningRejectsMissingVerificationEvidence(t *testing.T) {
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"internal/runtime/agent/runner.go"},
		Score:    0.9,
		Verified: true,
		// VerificationEvidence intentionally nil
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.State != LearningStateRejected || event.Reason != LearningReasonVerificationEvidenceMissing {
		t.Fatalf("event state/reason = %q/%q, want rejected/verification_evidence_missing", event.State, event.Reason)
	}
	if len(weights) != 0 {
		t.Fatalf("missing verification evidence must not update weights: %v", weights)
	}
}

func TestApplyVerifiedRankerLearningRejectsEmptyEditPaths(t *testing.T) {
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.State != LearningStateRejected || event.Reason != LearningReasonNoEditPaths {
		t.Fatalf("event state/reason = %q/%q, want rejected/no_edit_paths", event.State, event.Reason)
	}
	if len(weights) != 0 {
		t.Fatalf("empty edit paths must not update weights: %v", weights)
	}
}

func TestApplyVerifiedRankerLearningRejectsVerificationNotPassed(t *testing.T) {
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.State != LearningStateRejected || event.Reason != LearningReasonVerificationNotPassed {
		t.Fatalf("event state/reason = %q/%q, want rejected/verification_not_passed", event.State, event.Reason)
	}
	if len(weights) != 0 {
		t.Fatalf("failed verification must not update weights: %v", weights)
	}
}

func TestApplyVerifiedRankerLearningRejectsProtectedEditPaths(t *testing.T) {
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.State != LearningStateRejected || event.Reason != LearningReasonProtectedPathInEdits {
		t.Fatalf("event state/reason = %q/%q, want rejected/protected_path_in_edits", event.State, event.Reason)
	}
	if len(weights) != 0 {
		t.Fatalf("protected edit paths must not update weights: %v", weights)
	}
}

func TestApplyVerifiedRankerLearningRejectsEditPathMismatch(t *testing.T) {
	root := t.TempDir()
	event, weights, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if event.State != LearningStateRejected || event.Reason != LearningReasonEditPathMismatch {
		t.Fatalf("event state/reason = %q/%q, want rejected/edit_path_mismatch", event.State, event.Reason)
	}
	if len(weights) != 0 {
		t.Fatalf("mismatched edit paths must not update weights: %v", weights)
	}
}

func TestRecordVerifiedRankerEvidenceUnaffectedByVerificationGate(t *testing.T) {
	root := t.TempDir()
	// RecordVerifiedRankerEvidence does NOT require VerificationEvidence.
	event, err := RecordVerifiedRankerEvidence(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("record evidence: %v", err)
	}
	if event.State != LearningStateAccepted || event.Reason != LearningReasonVerifiedRecovery {
		t.Fatalf("recovery evidence should be accepted without verification gate, got state=%q reason=%q", event.State, event.Reason)
	}
}

// TestHasProtectedEditPath covers protected-path gate edge cases including the
// R1 (false positive on .idiom prefix substring) and R2 (./ bypass) fixes.
func TestHasProtectedEditPath(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		expected bool
	}{
		// Core protected cases (must remain protected)
		{
			name:     ".idiom/ directory",
			paths:    []string{".idiom/ranker/weights.json"},
			expected: true,
		},
		{
			name:     "bare .idiom",
			paths:    []string{".idiom"},
			expected: true,
		},
		{
			name:     "deep .idiom/ path",
			paths:    []string{".idiom/ranker/learning_log.jsonl"},
			expected: true,
		},
		// R1 regression: .idiom prefix substring must NOT be protected
		{
			name:     "R1: .idiom_backup is not .idiom",
			paths:    []string{".idiom_backup/foo.go"},
			expected: false,
		},
		{
			name:     "R1: .idiom_config is not .idiom",
			paths:    []string{".idiom_config/config.json"},
			expected: false,
		},
		{
			name:     "R1: .idiom-temp is not .idiom",
			paths:    []string{".idiom-temp/data.txt"},
			expected: false,
		},
		// R2 regression: ./ prefixed .idiom paths MUST be caught
		{
			name:     "R2: ./.idiom/ bypass via ./ prefix",
			paths:    []string{"./.idiom/ranker/weights.json"},
			expected: true,
		},
		{
			name:     "R2: ./.idiom bare bypass via ./ prefix",
			paths:    []string{"./.idiom"},
			expected: true,
		},
		{
			name:     "R2: ./.idiom/ deep path via ./ prefix",
			paths:    []string{"./.idiom/comprehension/graph.sqlite"},
			expected: true,
		},
		// Normal paths (must not be protected)
		{
			name:     "normal Go source",
			paths:    []string{"internal/runtime/agent/runner.go"},
			expected: false,
		},
		{
			name:     "multiple normal paths",
			paths:    []string{"pkg/safe.go", "cmd/idiom/main.go"},
			expected: false,
		},
		// Escape / absolute paths (must be protected — normalization rejects them)
		{
			name:     "../ escape",
			paths:    []string{"../escape.go"},
			expected: true,
		},
		{
			name:     "absolute path",
			paths:    []string{"/etc/passwd"},
			expected: true,
		},
		// Edge: empty or whitespace-only (skipped by loop, not protected)
		{
			name:     "empty string (skipped)",
			paths:    []string{""},
			expected: false,
		},
		{
			name:     "whitespace-only (skipped)",
			paths:    []string{"   "},
			expected: false,
		},
		// Mixed: normal path alongside a protected path → protected
		{
			name:     "mixed normal + protected .idiom/",
			paths:    []string{"pkg/safe.go", ".idiom/ranker/weights.json"},
			expected: true,
		},
		// Mixed: normal path alongside R1 false-positive path → NOT protected
		{
			name:     "mixed normal + .idiom_backup (R1)",
			paths:    []string{"pkg/safe.go", ".idiom_backup/foo.go"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasProtectedEditPath(tt.paths)
			if got != tt.expected {
				t.Errorf("hasProtectedEditPath(%v) = %v, want %v", tt.paths, got, tt.expected)
			}
		})
	}
}

// TestApplyVerifiedRankerLearningStoresVerificationEvidenceInLog verifies that
// the VerificationEvidence gate predicates (EditPaths, VerificationCommand,
// VerificationPassed) are persisted in the learning log entry so gate decisions
// are reconstructable from the audit trail. VerificationOutput is intentionally
// excluded from the log for privacy.
func TestApplyVerifiedRankerLearningStoresVerificationEvidenceInLog(t *testing.T) {
	root := t.TempDir()
	_ = os.Setenv(RankerLearningEnv, "1")
	defer os.Unsetenv(RankerLearningEnv)

	editPaths := []string{"pkg/target.go", "pkg/target_test.go"}
	entry, _, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("ApplyVerifiedRankerLearning: %v", err)
	}
	if entry.State != LearningStateAccepted {
		t.Fatalf("expected accepted, got %q", entry.State)
	}
	// Verification evidence must be present in the log entry.
	if entry.Verification == nil {
		t.Fatal("entry.Verification should not be nil")
	}
	if !entry.Verification.VerificationPassed {
		t.Error("entry.Verification.VerificationPassed should be true")
	}
	if entry.Verification.VerificationCommand != "go test ./..." {
		t.Errorf("entry.Verification.VerificationCommand = %q, want %q", entry.Verification.VerificationCommand, "go test ./...")
	}
	if len(entry.Verification.EditPaths) != 2 {
		t.Errorf("entry.Verification.EditPaths = %v, want 2 paths", entry.Verification.EditPaths)
	}

	// Read back from the learning log and verify the persisted entry.
	entries, err := ReadLearningLog(root)
	if err != nil {
		t.Fatalf("ReadLearningLog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("learning log should have at least one entry")
	}
	persisted := entries[0]
	if persisted.Verification == nil {
		t.Fatal("persisted entry.Verification should not be nil")
	}
	if !persisted.Verification.VerificationPassed {
		t.Error("persisted VerificationPassed should be true")
	}
	if len(persisted.Verification.EditPaths) != 2 {
		t.Errorf("persisted EditPaths = %v, want 2 paths", persisted.Verification.EditPaths)
	}
	if persisted.Verification.VerificationCommand != "go test ./..." {
		t.Errorf("persisted VerificationCommand = %q", persisted.Verification.VerificationCommand)
	}
}

// TestApplyVerifiedRankerLearningLogExcludesVerificationOutput verifies that
// VerificationOutput is NOT stored in the learning log's verification evidence.
// The log carries structured gate predicates only; raw command output stays out.
func TestApplyVerifiedRankerLearningLogExcludesVerificationOutput(t *testing.T) {
	root := t.TempDir()
	_ = os.Setenv(RankerLearningEnv, "1")
	defer os.Unsetenv(RankerLearningEnv)

	_, _, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("ApplyVerifiedRankerLearning: %v", err)
	}
	// Read back from the log.
	entries, err := ReadLearningLog(root)
	if err != nil {
		t.Fatalf("ReadLearningLog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("learning log should have at least one entry")
	}
	persisted := entries[0]
	if persisted.Verification == nil {
		t.Fatal("persisted entry.Verification should not be nil")
	}
	// The LearningLogVerificationEvidence type has no VerificationOutput field,
	// so this is a structural guarantee. The raw output must not leak into any
	// persisted field.
	raw, err := json.Marshal(persisted)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(raw), "some raw command output") {
		t.Error("raw VerificationOutput should not appear in persisted learning log entry")
	}
	if strings.Contains(string(raw), "verification_output") {
		t.Error("verification_output field should not appear in persisted learning log entry")
	}
}

// TestApplyVerifiedRankerLearningRejectedStoresVerificationEvidence verifies that
// even when the gate rejects a request, the verification evidence is still
// persisted so the rejection reason can be correlated with the evidence that
// triggered it.
func TestApplyVerifiedRankerLearningRejectedStoresVerificationEvidence(t *testing.T) {
	root := t.TempDir()
	_ = os.Setenv(RankerLearningEnv, "1")
	defer os.Unsetenv(RankerLearningEnv)

	// A request where verification did NOT pass — should be rejected.
	entry, _, err := ApplyVerifiedRankerLearning(root, VerifiedRankerLearningRequest{
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
	if err != nil {
		t.Fatalf("ApplyVerifiedRankerLearning: %v", err)
	}
	if entry.State != LearningStateRejected {
		t.Fatalf("expected rejected, got %q", entry.State)
	}
	if entry.Reason != LearningReasonVerificationNotPassed {
		t.Fatalf("expected reason verification_not_passed, got %q", entry.Reason)
	}
	// Verification evidence must still be logged so the audit trail shows what failed.
	if entry.Verification == nil {
		t.Fatal("entry.Verification should not be nil for rejected entries")
	}
	if entry.Verification.VerificationPassed {
		t.Error("entry.Verification.VerificationPassed should be false")
	}
	if len(entry.Verification.EditPaths) != 1 {
		t.Errorf("entry.Verification.EditPaths = %v, want 1 path", entry.Verification.EditPaths)
	}

	// Verify from persisted log.
	entries, err := ReadLearningLog(root)
	if err != nil {
		t.Fatalf("ReadLearningLog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("learning log should have at least one entry")
	}
	persisted := entries[0]
	if persisted.Verification == nil {
		t.Fatal("persisted entry.Verification should not be nil for rejected entries")
	}
	if persisted.Verification.VerificationPassed {
		t.Error("persisted VerificationPassed should be false")
	}
	if persisted.Verification.VerificationCommand != "go test ./..." {
		t.Errorf("persisted VerificationCommand = %q", persisted.Verification.VerificationCommand)
	}
}
