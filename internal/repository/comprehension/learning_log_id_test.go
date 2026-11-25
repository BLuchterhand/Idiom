package comprehension

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// orphanedLegacyLine is an event written in July 2026 by an id recipe that
// hashed the live struct; its id no longer matches its record.
const orphanedLegacyLine = `{"event_id":"ranker_cbcf28351f2f2a25","request_id":"request_f29a943754e06267","timestamp":"2026-07-12T20:57:07.808696Z","source":"eval","action":"boost","state":"rejected","reason":"no_edit_paths","score":0.7931034482758621,"task_id":"task_799779cf191d2125","evidence":{"kind":"swe_groundedness_oracle","verified":true,"task_id":"task_799779cf191d2125","metrics":{"boost_at":0.75,"min_score":0.55,"penalty_at":0.4,"score":0.7931034482758621}},"before_weights":{},"after_weights":{},"rollback":{"weights":{}},"verification":{"edit_paths":[],"verification_command":"swe_groundedness_oracle","verification_passed":true}}`

func goldenIDEntry() LearningLogEntry {
	after := map[string]float64{"a": 1.5}

	return LearningLogEntry{
		RequestID: "request_0123456789abcdef", Timestamp: "2026-01-02T03:04:05Z", Source: "eval", Action: "boost",
		State: "accepted", Reason: "verified_edit", Score: 0.75, Files: []string{"core/a.cpp"}, TaskID: "task_0123456789abcdef",
		BeforeWeights: &map[string]float64{}, AfterWeights: &after, Rollback: &LearningRollbackData{Weights: map[string]float64{}},
		Verification: &LearningLogVerificationEvidence{EditPaths: []string{"core/a.cpp"}, VerificationCommand: "go test ./...", VerificationPassed: true},
	}
}

// The id recipe is pinned: this value was produced before id_version existed.
func TestLearningEventIDRecipeIsPinned(t *testing.T) {
	if got := learningEventID(goldenIDEntry()); got != "ranker_7f0fe7d99284933b" {
		t.Fatalf("event id recipe changed: %s", got)
	}

	versioned := goldenIDEntry()
	versioned.IDVersion = learningEventIDVersion

	if learningEventID(versioned) != "ranker_7f0fe7d99284933b" {
		t.Fatal("id_version must not feed the hash")
	}
}

// A field added to LearningLogEntry changes nothing about existing ids, but
// whoever adds one must decide whether the recipe should cover it.
func TestLearningLogEntryFieldsMatchTheFrozenRecipe(t *testing.T) {
	names := func(v any, skip ...string) []string {
		var out []string

		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			if !slices.Contains(skip, typ.Field(i).Name) {
				out = append(out, typ.Field(i).Name)
			}
		}

		return out
	}

	if got, want := names(LearningLogEntry{}, "EventID", "IDVersion"), names(learningEventIDRecipeV1{}); !slices.Equal(got, want) {
		t.Fatalf("LearningLogEntry fields %v differ from the frozen recipe %v: decide whether a new recipe version is needed", got, want)
	}
}

func TestNewLearningEventsCarryIDVersion(t *testing.T) {
	entry, line, err := canonicalLearningDecision(LearningLogEntry{Source: "eval", Action: "boost", State: "rejected", Reason: "no_edit_paths", BeforeWeights: &map[string]float64{}, AfterWeights: &map[string]float64{}, Rollback: &LearningRollbackData{Weights: map[string]float64{}}, Verification: &LearningLogVerificationEvidence{VerificationCommand: "swe_groundedness_oracle", VerificationPassed: true}})
	if err != nil {
		t.Fatal(err)
	}

	if entry.IDVersion != learningEventIDVersion || !strings.Contains(string(line), `"id_version":1`) {
		t.Fatalf("new event lacks id_version: %s", line)
	}
}

func writeRankerLog(t *testing.T, content string) string {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "ranker")

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "learning_log.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return root
}

func TestOrphanedLegacyLineIsQuarantined(t *testing.T) {
	root := writeRankerLog(t, orphanedLegacyLine+"\n")

	entries, err := ReadLearningLog(context.Background(), root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read = %v, %v; the orphaned no-op line must be quarantined, not fail the log", entries, err)
	}

	quarantined, _ := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.quarantine.jsonl"))
	if strings.TrimSpace(string(quarantined)) != orphanedLegacyLine {
		t.Fatalf("quarantine file = %q", quarantined)
	}

	live, _ := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	if len(live) != 0 {
		t.Fatalf("log still holds %q", live)
	}
}

func TestOrphanedLineThatChangedWeightsIsNotQuarantined(t *testing.T) {
	root := t.TempDir()

	entry, _, err := ApplyManualRankerNudge(context.Background(), root, "boost", []string{"core/a.cpp"}, "", "")
	if err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl")

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(raw), `"after_weights":{"`) {
		t.Fatalf("fixture must change weights: %s", raw)
	}

	if err := os.WriteFile(logPath, []byte(strings.Replace(string(raw), entry.EventID, "ranker_0000000000000000", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadLearningLog(context.Background(), root); err == nil {
		t.Fatal("an orphaned event that changed weights must still fail the strict read")
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "learning_log.quarantine.jsonl")); err == nil {
		t.Fatal("nothing should have been quarantined")
	}
}
