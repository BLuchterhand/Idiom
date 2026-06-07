package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunSkillsListIncludesOrchestratorWorkflow verifies the
// canonical orchestrator-workflow skill (deferred from Phase 3)
// ships in the embedded set and is discoverable via `idiom skills
// list`. If the skill isn't listed, install will also fail.
func TestRunSkillsListIncludesOrchestratorWorkflow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunMain([]string{"list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("skills list must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "orchestrator-workflow") {
		t.Fatalf("skills list should include orchestrator-workflow: %s", stdout.String())
	}
}

// TestRunSkillsInstallCopiesSkillToProjectRoot is the canonical
// happy path: installing a known skill writes the embedded SKILL.md
// to <project-root>/<target>/<name>/SKILL.md and exits 0.
func TestRunSkillsInstallCopiesSkillToProjectRoot(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"install", "--project-root", root, "orchestrator-workflow"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	dest := filepath.Join(root, ".claude", "skills", "orchestrator-workflow", "SKILL.md")
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read installed SKILL.md: %v", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		t.Fatalf("installed SKILL.md is empty — embed produced nothing")
	}
	if !bytes.Contains(body, []byte("Orchestrator Workflow")) {
		t.Fatalf("installed SKILL.md missing canonical content header: %s", body[:min(200, len(body))])
	}
}

// TestRunSkillsInstallRespectsCustomTarget covers the orchestrator-
// agnostic seam: --target swaps .claude/skills for whatever
// convention the project uses (e.g. .codex/skills). The canonical
// content is unchanged; only the destination shifts.
func TestRunSkillsInstallRespectsCustomTarget(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"install", "--project-root", root, "--target", ".codex/skills", "orchestrator-workflow"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install with --target must exit 0; got %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "skills", "orchestrator-workflow", "SKILL.md")); err != nil {
		t.Fatalf("custom target should land file at .codex/skills/...: %v", err)
	}
}

// TestRunSkillsInstallRefusesOverwriteWithoutForce verifies the
// safety net: a pre-existing SKILL.md is exit 2 with a "use
// --force" hint. The canonical version is starting-point content
// that operators customize; silent overwrites would lose that work.
func TestRunSkillsInstallRefusesOverwriteWithoutForce(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "orchestrator-workflow")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sentinel := []byte("CUSTOMIZED-BY-OPERATOR")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), sentinel, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"install", "--project-root", root, "orchestrator-workflow"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("existing SKILL.md without --force must exit 2; got %d", code)
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Fatalf("stderr should suggest --force: %s", stderr.String())
	}
	preserved, _ := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if !bytes.Equal(preserved, sentinel) {
		t.Fatalf("file should be untouched on refusal; got %q", preserved)
	}
}

// TestRunSkillsInstallForceOverwrites verifies --force does what it
// says: overwrites the existing file with the canonical body.
func TestRunSkillsInstallForceOverwrites(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "orchestrator-workflow")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("OLD"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"install", "--project-root", root, "--force", "orchestrator-workflow"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--force install must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	body, _ := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if bytes.Equal(body, []byte("OLD")) {
		t.Fatalf("--force should overwrite; file still has old content")
	}
}

// TestRunSkillsInstallRejectsUnknownSkill verifies a clear error
// when the requested skill isn't in the embedded set. Exit 1 (not
// found) distinguishes from exit 2 (flag/usage).
func TestRunSkillsInstallRejectsUnknownSkill(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"install", "--project-root", root, "no-such-skill"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("unknown skill must exit 1; got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown skill") {
		t.Fatalf("stderr should explain: %s", stderr.String())
	}
}

// TestRunSkillsInstallRejectsPathTraversalName guards the security
// surface: a skill name with path separators or `..` could escape
// the install dir. Reject before any filesystem read.
func TestRunSkillsInstallRejectsPathTraversalName(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"../../etc/passwd", "a/b", "..", "x\x00y"} {
		var stdout, stderr bytes.Buffer
		code := RunMain([]string{"install", "--project-root", root, bad}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("bad name %q must exit 2; got %d", bad, code)
		}
	}
}
