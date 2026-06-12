package productworkflow

import (
	"os"
	"path/filepath"
	"testing"
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

func writeProfileFile(t *testing.T, profilesDir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(profilesDir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func createProfilesDir(t *testing.T, projectRoot string) string {
	t.Helper()

	dir := filepath.Join(projectRoot, ".idiom", "research", "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	return dir
}

func TestDiscoverProfilesReturnsSortedValidProfiles(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	writeProfileFile(t, dir, "b.json", validResearchProfileJSONWithID("beta"))
	writeProfileFile(t, dir, "a.json", validResearchProfileJSONWithID("alpha"))

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(result.Profiles))
	}

	if len(result.Findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(result.Findings))
	}

	if result.Profiles[0].ProfileID != "alpha" {
		t.Fatalf("expected first profile_id to be alpha (sorted by filename a.json), got %q", result.Profiles[0].ProfileID)
	}

	if result.Profiles[1].ProfileID != "beta" {
		t.Fatalf("expected second profile_id to be beta (sorted by filename b.json), got %q", result.Profiles[1].ProfileID)
	}
}

func TestDiscoverProfilesEmptyDirReturnsNoError(t *testing.T) {
	root := t.TempDir()
	createProfilesDir(t, root)

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(result.Profiles))
	}

	if len(result.Findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(result.Findings))
	}
}

func TestDiscoverProfilesMissingDirReturnsNoError(t *testing.T) {
	root := t.TempDir()

	result, err := DiscoverProfiles(filepath.Join(root, "nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(result.Profiles))
	}

	if len(result.Findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(result.Findings))
	}
}

func TestDiscoverProfilesRejectsInvalidJSON(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	writeProfileFile(t, dir, "bad.json", `{not valid json`)

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(result.Profiles))
	}

	found := false

	for _, f := range result.Findings {
		if f.Kind == "validation_error" && f.Severity == "error" {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected invalid_json error finding, got: %#v", result.Findings)
	}
}

func TestDiscoverProfilesRejectsMissingSchemaVersion(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	writeProfileFile(t, dir, "missing_sv.json", `{
  "schema_version": "",
  "name": "test-profile",
  "researchers": [],
  "merge_policy": {}
}`)

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(result.Profiles))
	}

	found := false

	for _, f := range result.Findings {
		if f.Kind == "validation_error" && f.Severity == "error" {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected validation_error finding, got: %#v", result.Findings)
	}
}

func TestDiscoverProfilesRejectsMissingName(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	writeProfileFile(t, dir, "missing_name.json", `{
  "schema_version": "idiom.research_profile.v1",
  "name": "",
  "profile_id": "",
  "researchers": [],
  "merge_policy": {}
}`)

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(result.Profiles))
	}

	found := false

	for _, f := range result.Findings {
		if f.Kind == "validation_error" && f.Severity == "error" {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected validation_error finding for missing name/profile_id, got: %#v", result.Findings)
	}
}

func TestDiscoverProfilesRejectsAbsolutePathFilename(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	escPath := filepath.Join(dir, "absolute.json")

	if err := os.WriteFile(escPath, []byte(validResearchProfileJSONWithID("esc")), 0o644); err != nil {
		t.Fatalf("write absolute.json: %v", err)
	}

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false

	for _, f := range result.Findings {
		if f.Kind == "path_traversal" && f.Severity == "error" {
			found = true
		}
	}

	if found {
		t.Fatalf("did not expect path_traversal for normal filename, got: %#v", result.Findings)
	}

	if len(result.Profiles) != 1 {
		t.Fatalf("expected 1 profile for normal filename, got %d", len(result.Profiles))
	}
}

func TestDiscoverProfilesAllowsDoubleDotInMiddle(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	writeProfileFile(t, dir, "my..notes.json", validResearchProfileJSONWithID("dbl"))

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 1 {
		t.Fatalf("expected 1 profile for filename with '..' in middle, got %d", len(result.Profiles))
	}

	found := false

	for _, f := range result.Findings {
		if f.Kind == "path_traversal" {
			found = true
		}
	}

	if found {
		t.Fatalf("filename with '..' in middle should not be flagged as path traversal, got: %#v", result.Findings)
	}
}

func TestDiscoverProfilesSkipsNonJSON(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	writeProfileFile(t, dir, "notes.txt", "some text")

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(result.Profiles))
	}

	found := false

	for _, f := range result.Findings {
		if f.Kind == "non_json_file" && f.Severity == "warning" {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected non_json_file warning finding, got: %#v", result.Findings)
	}
}

func TestDiscoverProfilesRejectsSymlinkProfiles(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	outside := filepath.Join(t.TempDir(), "outside.json")

	if err := os.WriteFile(outside, []byte(validResearchProfileJSONWithID("outside")), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(dir, "linked.json")); err != nil {
		t.Fatal(err)
	}

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 0 {
		t.Fatalf("expected symlinked profile to be rejected, got %d profile(s)", len(result.Profiles))
	}

	found := false

	for _, f := range result.Findings {
		if f.Kind == "profile_symlink" && f.Severity == "error" {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected profile_symlink error finding, got: %#v", result.Findings)
	}
}

func TestDiscoverProfilesGeneratesProfileIDFromFilename(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
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
	writeProfileFile(t, dir, "my-profile.json", profileNoID)

	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(result.Profiles))
	}

	if result.Profiles[0].ProfileID != "my-profile" {
		t.Fatalf("expected profile_id \"my-profile\", got %q", result.Profiles[0].ProfileID)
	}
}
