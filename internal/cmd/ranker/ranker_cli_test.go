package ranker
import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/comprehension"
)

func TestRunRankerRollbackRestoresWeightsFromLearningEvent(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{"workspace":{"root":%q}}`, root)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	event, weights, err := comprehension.ApplyVerifiedRankerLearning(root, comprehension.VerifiedRankerLearningRequest{
		Source:   "eval",
		Action:   "boost",
		Files:    []string{"internal/runner.go"},
		Score:    0.9,
		Verified: true,
	})
	if err != nil {
		t.Fatalf("apply learning: %v", err)
	}
	if weights["internal/runner.go"] <= comprehension.LearnedWeightBaseline {
		t.Fatalf("expected boosted weight before rollback, got %v", weights)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"rollback", "--config", configPath, "--event", event.EventID}, &stdout, &stderr, nil, Deps{})
	if code != 0 {
		t.Fatalf("rollback failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "ranker weights restored from learning event "+event.EventID) {
		t.Fatalf("rollback output missing event id: %s", stdout.String())
	}
	restored, err := comprehension.LoadLearnedWeights(root)
	if err != nil {
		t.Fatalf("load restored: %v", err)
	}
	if len(restored) != 0 {
		t.Fatalf("rollback should restore empty baseline weights, got %v", restored)
	}
	entries, _ := comprehension.ReadLearningLog(root)
	summary := comprehension.SummarizeLearningLog(entries)
	if summary.RestoreCount != 1 || summary.AcceptedReasonCounts[comprehension.LearningReasonRollbackRestore] != 1 {
		t.Fatalf("restore log summary missing: %#v", summary)
	}
}

func TestRunRankerManualNoteIsSanitizedInLearningLog(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{"workspace":{"root":%q}}`, root)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	sentinel := "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content customer freeform"
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"boost",
		"--config", configPath,
		"--note", sentinel,
		"internal/runner.go",
	}, &stdout, &stderr, nil, Deps{})
	if code != 0 {
		t.Fatalf("boost failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	if err != nil {
		t.Fatalf("read learning log: %v", err)
	}
	body := strings.ToLower(string(data))
	for _, marker := range []string{"raw_prompt", "begin openssh", "transcript", "protected content", "customer freeform"} {
		if strings.Contains(body, marker) {
			t.Fatalf("manual note marker %q leaked into learning log: %s", marker, body)
		}
	}
	if !strings.Contains(body, `"note":"`+comprehension.LearningLogNotePresent+`"`) {
		t.Fatalf("learning log should retain bounded note presence, got: %s", body)
	}

	entries, err := comprehension.ReadLearningLog(root)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Note != comprehension.LearningLogNotePresent {
		t.Fatalf("unexpected sanitized note entries: %#v", entries)
	}
	summary := comprehension.SummarizeLearningLog(entries)
	if summary.BoostCount != 1 || summary.BySource["cli"] != 1 {
		t.Fatalf("manual ranker counts missing after note sanitization: %#v", summary)
	}
}

func TestRunRankerRollbackRequiresEvent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"rollback"}, &stdout, &stderr, nil, Deps{})
	if code != 2 {
		t.Fatalf("expected usage exit for missing event, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--event is required") {
		t.Fatalf("missing event error not surfaced: %s", stderr.String())
	}
}
