package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

func validResearchProfileJSONWithID(id string) string {
	return `{
  "schema_version": "idiom.research_profile.v1",
  "name": "` + id + `",
  "profile_id": "` + id + `",
  "researchers": [
    {
      "id": "r1",
      "alignment": "north_star",
      "question": "test question",
      "required_sources": [],
      "allowed_capabilities": ["context_query"],
      "output": "evidence_cards"
    }
  ],
  "merge_policy": {
    "conflicts": "preserve_until_adr",
    "unsupported_claims": "block_prd_audit",
    "stale_local_context": "refresh_or_mark_gap"
  }
}`
}

func writeProfileFile(profilesDir, name, content string) {
	ginkgo.GinkgoHelper()
	{

		err := os.WriteFile(filepath.Join(profilesDir, name), []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", name, err))
	}

}

func createProfilesDir(projectRoot string) string {
	ginkgo.GinkgoHelper()

	dir := filepath.Join(projectRoot, ".idiom", "research", "profiles")
	{
		err := os.MkdirAll(dir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", dir, err))
	}

	return dir
}

var _ = ginkgo.Describe("research profile store", func() {
	ginkgo.It("DiscoverProfilesReturnsSortedValidProfiles", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		writeProfileFile(dir, "b.json", validResearchProfileJSONWithID("beta"))
		writeProfileFile(dir, "a.json", validResearchProfileJSONWithID("alpha"))

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected 2 profiles, got %d", len(result.Profiles)))
		gomega.Expect(len(result.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 findings, got %d", len(result.Findings)))
		gomega.Expect(result.Profiles[0].ProfileID != "alpha").To(gomega.BeFalse(), fmt.Sprintf("expected first profile_id to be alpha (sorted by filename a.json), got %q", result.Profiles[0].ProfileID))
		gomega.Expect(result.Profiles[1].ProfileID != "beta").To(gomega.BeFalse(), fmt.Sprintf("expected second profile_id to be beta (sorted by filename b.json), got %q", result.Profiles[1].ProfileID))

	})
	ginkgo.It("DiscoverProfilesEmptyDirReturnsNoError", func() {
		root := ginkgo.GinkgoT().TempDir()
		createProfilesDir(root)

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 profiles, got %d", len(result.Profiles)))
		gomega.Expect(len(result.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 findings, got %d", len(result.Findings)))

	})
	ginkgo.It("DiscoverProfilesMissingDirReturnsNoError", func() {
		root := ginkgo.GinkgoT().TempDir()

		result, err := DiscoverProfiles(filepath.Join(root, "nonexistent"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 profiles, got %d", len(result.Profiles)))
		gomega.Expect(len(result.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 findings, got %d", len(result.Findings)))

	})
	ginkgo.It("DiscoverProfilesRejectsInvalidJSON", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		writeProfileFile(dir, "bad.json", `{not valid json`)

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 profiles, got %d", len(result.Profiles)))

		found := false

		for _, f := range result.Findings {
			if f.Kind == "validation_error" && f.Severity == "error" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_json error finding, got: %#v", result.Findings))

	})
	ginkgo.It("DiscoverProfilesRejectsMissingSchemaVersion", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		writeProfileFile(dir, "missing_sv.json", `{
  "schema_version": "",
  "name": "test-profile",
  "researchers": [],
  "merge_policy": {}
}`)

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 profiles, got %d", len(result.Profiles)))

		found := false

		for _, f := range result.Findings {
			if f.Kind == "validation_error" && f.Severity == "error" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected validation_error finding, got: %#v", result.Findings))

	})
	ginkgo.It("DiscoverProfilesRejectsMissingName", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		writeProfileFile(dir, "missing_name.json", `{
  "schema_version": "idiom.research_profile.v1",
  "name": "",
  "profile_id": "",
  "researchers": [],
  "merge_policy": {}
}`)

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 profiles, got %d", len(result.Profiles)))

		found := false

		for _, f := range result.Findings {
			if f.Kind == "validation_error" && f.Severity == "error" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected validation_error finding for missing name/profile_id, got: %#v", result.Findings))

	})
	ginkgo.It("DiscoverProfilesRejectsAbsolutePathFilename", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		escPath := filepath.Join(dir, "absolute.json")
		{

			err := os.WriteFile(escPath, []byte(validResearchProfileJSONWithID("esc")), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write absolute.json: %v", err))
		}

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))

		found := false

		for _, f := range result.Findings {
			if f.Kind == "path_traversal" && f.Severity == "error" {
				found = true
			}
		}
		gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprintf("did not expect path_traversal for normal filename, got: %#v", result.Findings))
		gomega.Expect(len(result.Profiles) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 profile for normal filename, got %d", len(result.Profiles)))

	})
	ginkgo.It("DiscoverProfilesAllowsDoubleDotInMiddle", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		writeProfileFile(dir, "my..notes.json", validResearchProfileJSONWithID("dbl"))

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 profile for filename with '..' in middle, got %d", len(result.Profiles)))

		found := false

		for _, f := range result.Findings {
			if f.Kind == "path_traversal" {
				found = true
			}
		}
		gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprintf("filename with '..' in middle should not be flagged as path traversal, got: %#v", result.Findings))

	})
	ginkgo.It("DiscoverProfilesSkipsNonJSON", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		writeProfileFile(dir, "notes.txt", "some text")

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 profiles, got %d", len(result.Profiles)))

		found := false

		for _, f := range result.Findings {
			if f.Kind == "non_json_file" && f.Severity == "warning" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected non_json_file warning finding, got: %#v", result.Findings))

	})
	ginkgo.It("DiscoverProfilesRejectsSymlinkProfiles", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		outside := filepath.Join(ginkgo.GinkgoT().TempDir(), "outside.json")
		{

			err := os.WriteFile(outside, []byte(validResearchProfileJSONWithID("outside")), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}
		{

			err := os.Symlink(outside, filepath.Join(dir, "linked.json"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected symlinked profile to be rejected, got %d profile(s)", len(result.Profiles)))

		found := false

		for _, f := range result.Findings {
			if f.Kind == "profile_symlink" && f.Severity == "error" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected profile_symlink error finding, got: %#v", result.Findings))

	})
	ginkgo.It("DiscoverProfilesGeneratesProfileIDFromFilename", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := createProfilesDir(root)
		profileNoID := `{
  "schema_version": "idiom.research_profile.v1",
  "name": "test-profile",
  "researchers": [
    {
      "id": "r1",
      "alignment": "north_star",
      "question": "test question",
      "required_sources": [],
      "allowed_capabilities": ["context_query"],
      "output": "evidence_cards"
    }
  ],
  "merge_policy": {
    "conflicts": "preserve_until_adr",
    "unsupported_claims": "block_prd_audit",
    "stale_local_context": "refresh_or_mark_gap"
  }
}`
		writeProfileFile(dir, "my-profile.json", profileNoID)

		result, err := DiscoverProfiles(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(len(result.Profiles) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 profile, got %d", len(result.Profiles)))
		gomega.Expect(result.Profiles[0].ProfileID != "my-profile").To(gomega.BeFalse(), fmt.Sprintf("expected profile_id \"my-profile\", got %q", result.Profiles[0].ProfileID))

	})
})
