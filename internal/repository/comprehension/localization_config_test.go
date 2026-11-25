package comprehension

import (
	"context"
	"encoding/json"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Golden values below were produced by the commit before the localization
// contract (b69fef016); the default config must keep reproducing them.
const (
	goldenDefaultViewKeyID     = "aaad15c438f948f6"
	goldenDefaultPackFileJSON  = `{"path":"a.go","role":"editable","reason":"r","score":1.5,"ranker_reasons":[{"reason":"role_floor","state":"x","score_delta":3}]}`
	goldenDefaultCollectorHash = "17eb4d700449e2cd"
)

func writeLocalizationConfig(root string, section map[string]any) {
	data, err := json.Marshal(map[string]any{"app_name": "x", "localization": section})
	Expect(err).NotTo(HaveOccurred())
	writeFile(filepath.Join(root, ".idiom", "config.json"), string(data))
}

var _ = Describe("localization contract defaults", func() {
	It("keeps default output byte-identical to the pre-contract golden", func() {
		root := GinkgoT().TempDir()
		key := viewKeyFromOptions(PackOptions{Task: "change day clock", Path: "core", Limit: 8}, "idx", "plan", IntentExplain,
			ComprehensionStateFingerprintSet{WorkspaceStateFingerprint: "ws", RankerWeightsFingerprint: "rw", RuntimeTracesFingerprint: "rt"},
			LocalizationConfigFingerprint(root))
		Expect(key.ID()).To(Equal(goldenDefaultViewKeyID))

		data, err := json.Marshal(PackFile{Path: "a.go", Role: "editable", Reason: "r", Score: 1.5,
			RankerReasons: []RankerReason{{Reason: "role_floor", State: "x", ScoreDelta: 3}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(goldenDefaultPackFileJSON))

		Expect(currentCollectorVersion(root)).To(Equal(goldenDefaultCollectorHash))
		Expect(currentCollectorVersionMap(root)).NotTo(HaveKey("localization"))
	})

	It("treats a missing section and an empty section as the zero config", func() {
		root := GinkgoT().TempDir()
		writeFile(filepath.Join(root, ".idiom", "config.json"), `{"app_name":"x"}`)
		Expect(LocalizationConfigFingerprint(root)).To(BeEmpty())
		writeLocalizationConfig(root, map[string]any{})
		Expect(LocalizationConfigFingerprint(root)).To(BeEmpty())
	})

	It("ignores list order and duplicates when fingerprinting", func() {
		a := LocalizationConfig{ContentGlobs: []string{"b/**", "a/**"}}
		b := LocalizationConfig{ContentGlobs: []string{"a/**", " b/** ", "a/**"}}
		Expect(a.Fingerprint()).NotTo(BeEmpty())
		Expect(a.Fingerprint()).To(Equal(b.Fingerprint()))
	})
})

var _ = Describe("localization config staleness", func() {
	configEdits := map[string]map[string]any{
		"content_globs":    {"content_globs": []string{"content/**/*.json"}},
		"shader_globs":     {"shader_globs": []string{"game/**/*.gdshader"}},
		"extra_extensions": {"extra_extensions": []string{".ps1"}},
		"key_patterns":     {"key_patterns": []string{`set\("(\w+)"\)`}},
		"key_min_len":      {"key_min_len": 3},
		"key_edges":        {"key_edges": true},
		"doc_roots":        {"doc_roots": []string{"docs/design"}},
	}

	for field, section := range configEdits {
		It("marks the index stale and misses the pack cache after editing "+field, func() {
			root := GinkgoT().TempDir()
			writeFile(filepath.Join(root, "go.mod"), "module example.com/locstale\n\ngo 1.22\n")
			writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
			_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
			Expect(err).NotTo(HaveOccurred())

			status, err := Status(root)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Fresh).To(BeTrue())
			before := viewKeyFromOptions(PackOptions{Task: "t", Limit: 8}, status.IndexID, "plan", IntentExplain,
				ComprehensionStateFingerprintSet{}, LocalizationConfigFingerprint(root))

			writeLocalizationConfig(root, section)

			status, err = Status(root)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.CollectorVersionMismatch).To(BeTrue(), "config edit must stale the index: %#v", status)
			Expect(status.Fresh).To(BeFalse())

			after := viewKeyFromOptions(PackOptions{Task: "t", Limit: 8}, status.IndexID, "plan", IntentExplain,
				ComprehensionStateFingerprintSet{}, LocalizationConfigFingerprint(root))
			Expect(after.ID()).NotTo(Equal(before.ID()), "pack cache key must change with the config")

			// The freshness decision rebuilds the root on a collector mismatch.
			decision, err := EnsureFresh(context.Background(), FreshnessRequest{
				Root: root, Operation: "context_pack", Policy: FreshnessReadThrough, Language: "go", MaxFiles: 200,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.Refresh).NotTo(BeNil())
			Expect(decision.Refresh.Reason).To(BeEquivalentTo(FreshnessReasonCollectorVersionMismatch))

			status, err = Status(root)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Fresh).To(BeTrue(), "rebuilt index must be fresh under the new config: %#v", status)
		})
	}

	It("stales the index when the config file becomes malformed", func() {
		root := GinkgoT().TempDir()
		writeLocalizationConfig(root, map[string]any{"doc_roots": []string{"docs"}})
		valid := currentCollectorVersion(root)
		writeFile(filepath.Join(root, ".idiom", "config.json"), `{"localization": {`)
		Expect(currentCollectorVersion(root)).NotTo(Equal(valid))
		Expect(LocalizationConfigFingerprint(root)).To(HavePrefix("invalid-"))
	})
})
