package productworkflow

import (
	"os"
	"path/filepath"
	"testing"
)

func validResearchProfileJSON() string {
	return `{
  "schema_version": "idiom.research_profile.v1",
  "name": "test-profile",
  "profile_id": "test",
  "researchers": [
    {
      "id": "r1",
      "alignment": "north_star",
      "question": "test question",
      "required_sources": [],
      "allowed_capabilities": [],
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
	writeProfileFile(t, dir, "b.json", validResearchProfileJSON())
	writeProfileFile(t, dir, "a.json", validResearchProfileJSON())
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
		if f.Kind == "invalid_json" && f.Severity == "error" {
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
		if f.Kind == "invalid_schema_version" && f.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected invalid_schema_version error finding, got: %#v", result.Findings)
	}
}

func TestDiscoverProfilesRejectsMissingName(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	writeProfileFile(t, dir, "missing_name.json", `{
  "schema_version": "idiom.research_profile.v1",
  "name": "",
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
		if f.Kind == "missing_name" && f.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing_name error finding, got: %#v", result.Findings)
	}
}

func TestDiscoverProfilesRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	dir := createProfilesDir(t, root)
	escPath := filepath.Join(dir, "..escape.json")
	if err := os.WriteFile(escPath, []byte(validResearchProfileJSON()), 0o644); err != nil {
		t.Fatalf("write ..escape.json: %v", err)
	}
	result, err := DiscoverProfiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(result.Profiles))
	}
	found := false
	for _, f := range result.Findings {
		if f.Kind == "path_traversal" && f.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected path_traversal error finding, got: %#v", result.Findings)
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
      "allowed_capabilities": [],
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
