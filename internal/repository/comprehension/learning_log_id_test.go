package comprehension

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func writeRankerLog(content string) string {
	root := GinkgoT().TempDir()
	dir := filepath.Join(root, ".idiom", "ranker")

	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "learning_log.jsonl"), []byte(content), 0o644)).To(Succeed())

	return root
}

var _ = Describe("learning event ids", func() {
	// The id recipe is pinned: this value was produced before id_version existed.
	It("pins the id recipe", func() {
		Expect(learningEventID(goldenIDEntry())).To(Equal("ranker_7f0fe7d99284933b"), "event id recipe changed")

		versioned := goldenIDEntry()
		versioned.IDVersion = learningEventIDVersion

		Expect(learningEventID(versioned)).To(Equal("ranker_7f0fe7d99284933b"), "id_version must not feed the hash")
	})

	// A field added to LearningLogEntry changes nothing about existing ids, but
	// whoever adds one must decide whether the recipe should cover it.
	It("keeps LearningLogEntry fields matching the frozen recipe", func() {
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

		Expect(names(LearningLogEntry{}, "EventID", "IDVersion")).To(Equal(names(learningEventIDRecipeV1{})),
			"decide whether a new recipe version is needed")
	})

	It("stamps new events with the id version", func() {
		entry, line, err := canonicalLearningDecision(LearningLogEntry{Source: "eval", Action: "boost", State: "rejected", Reason: "no_edit_paths", BeforeWeights: &map[string]float64{}, AfterWeights: &map[string]float64{}, Rollback: &LearningRollbackData{Weights: map[string]float64{}}, Verification: &LearningLogVerificationEvidence{VerificationCommand: "swe_groundedness_oracle", VerificationPassed: true}})
		Expect(err).NotTo(HaveOccurred())

		Expect(entry.IDVersion).To(Equal(learningEventIDVersion))
		Expect(string(line)).To(ContainSubstring(`"id_version":1`))
	})
})

var _ = Describe("orphaned learning log lines", func() {
	It("quarantines an orphaned legacy no-op line instead of failing the log", func() {
		root := writeRankerLog(orphanedLegacyLine + "\n")

		entries, err := ReadLearningLog(context.Background(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())

		quarantined, _ := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.quarantine.jsonl"))
		Expect(strings.TrimSpace(string(quarantined))).To(Equal(orphanedLegacyLine))

		live, _ := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
		Expect(live).To(BeEmpty(), "log still holds a line")
	})

	It("does not quarantine an orphaned line that changed weights", func() {
		root := GinkgoT().TempDir()

		entry, _, err := ApplyManualRankerNudge(context.Background(), root, "boost", []string{"core/a.cpp"}, "", "")
		Expect(err).NotTo(HaveOccurred())

		logPath := filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl")

		raw, err := os.ReadFile(logPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"after_weights":{"`), "fixture must change weights")

		Expect(os.WriteFile(logPath, []byte(strings.Replace(string(raw), entry.EventID, "ranker_0000000000000000", 1)), 0o644)).To(Succeed())

		_, err = ReadLearningLog(context.Background(), root)
		Expect(err).To(HaveOccurred(), "an orphaned event that changed weights must still fail the strict read")

		_, err = os.Stat(filepath.Join(root, ".idiom", "ranker", "learning_log.quarantine.jsonl"))
		Expect(err).To(HaveOccurred(), "nothing should have been quarantined")
	})
})
