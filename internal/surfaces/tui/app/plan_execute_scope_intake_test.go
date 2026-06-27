package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
)

func TestPlanExecuteScopeIntakeClassifiesMutationTargetsAsRequiredClosedScope(t *testing.T) {
	prompt := "Please create docs/foo.md, update docs/bar.md, and append docs/changelog.md. Do not edit any other files."

	intake := classifyPlanExecuteScopeIntake(prompt, scopeIntakePlanExecutePlan())

	for _, want := range []string{"docs/foo.md", "docs/bar.md", "docs/changelog.md"} {
		if !slices.Contains(intake.RequiredTargets, want) {
			t.Fatalf("required mutation targets missing %q: %#v", want, intake.RequiredTargets)
		}
	}

	if !intake.ClosedScope {
		t.Fatalf("closed scope was not detected")
	}
}

func TestPlanExecuteScopeIntakeDoesNotRequireReferenceOrEvidencePaths(t *testing.T) {
	prompt := "Use docs/reference.md as evidence and read docs/context.md before updating docs/target.md."

	intake := classifyPlanExecuteScopeIntake(prompt, scopeIntakePlanExecutePlan())

	if !slices.Contains(intake.RequiredTargets, "docs/target.md") {
		t.Fatalf("mutation target missing from required targets: %#v", intake.RequiredTargets)
	}

	for _, notWant := range []string{"docs/reference.md", "docs/context.md"} {
		if slices.Contains(intake.RequiredTargets, notWant) {
			t.Fatalf("reference/evidence path %q must not be required: %#v", notWant, intake.RequiredTargets)
		}
	}
}

func TestPlanExecuteScopeIntakeMutationMarkdownSkipsPlanDocumentResolution(t *testing.T) {
	root := t.TempDir()
	writeScopeIntakeFile(t, root, "docs/foo.md", "# Existing Plan\n\n## Files to Modify\n\n- pkg/other.go\n")

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	prompt := "create/update/append docs/foo.md. Do not edit any other files."
	required, allowed, docs, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, scopeIntakePlanExecutePlan(), cfg)

	if !slices.Contains(required, "docs/foo.md") {
		t.Fatalf("mutation markdown target missing from required paths: required=%#v allowed=%#v", required, allowed)
	}

	if len(docs) != 0 {
		t.Fatalf("mutation markdown target must not be resolved as a plan document: %#v", docs)
	}

	if len(blockers) != 0 {
		t.Fatalf("mutation markdown target must not produce plan-document blockers: %#v", blockers)
	}
}

func TestPlanExecuteScopeIntakeReferenceMarkdownDoesNotBecomeRequired(t *testing.T) {
	root := t.TempDir()
	writeScopeIntakeFile(t, root, "docs/reference.md", "# Reference\n\nUse this as background.\n")
	writeScopeIntakeFile(t, root, "docs/target.md", "# Target\n")

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	prompt := "Use docs/reference.md as evidence before updating docs/target.md. Do not edit any other files."
	required, _, _, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, scopeIntakePlanExecutePlan(), cfg)

	if !slices.Contains(required, "docs/target.md") {
		t.Fatalf("mutation target missing from required paths: %#v", required)
	}

	if slices.Contains(required, "docs/reference.md") {
		t.Fatalf("reference markdown path must not become required: %#v", required)
	}

	if len(blockers) != 0 {
		t.Fatalf("reference markdown should not block scope intake: %#v", blockers)
	}
}

func TestPlanExecuteScopeIntakeAuthorizesOnlyTypedRuntimeBriefNewFilePaths(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	explicit := implementationDispatchCompilePlanExecuteScopeIntake(
		"Create new file docs/new-note.md with the smoke result. Do not edit any other files.",
		scopeIntakePlanExecutePlan(),
		cfg,
	)
	if !slices.Contains(explicit.RequiredTargets, "docs/new-note.md") {
		t.Fatalf("explicit new file path missing from required targets: %#v", explicit.RequiredTargets)
	}
	if len(explicit.AuthorizedNewFiles) != 0 {
		t.Fatalf("prompt prose must not authorize new files: %#v", explicit.AuthorizedNewFiles)
	}

	typed := scopeIntakePlanExecutePlan()
	typed.runtimeBrief.AuthorizedNewFiles = []string{"docs/new-note.md"}
	authorized := implementationDispatchCompilePlanExecuteScopeIntake(
		"Create new file docs/new-note.md with the smoke result. Do not edit any other files.",
		typed,
		cfg,
	)
	if !slices.Contains(authorized.AuthorizedNewFiles, "docs/new-note.md") {
		t.Fatalf("typed runtime brief new file path missing from authorized new files: %#v", authorized.AuthorizedNewFiles)
	}

	unspecified := implementationDispatchCompilePlanExecuteScopeIntake(
		"Create a new markdown note with the smoke result. Do not edit any other files.",
		scopeIntakePlanExecutePlan(),
		cfg,
	)
	if len(unspecified.RequiredTargets) != 0 {
		t.Fatalf("unspecified new-file prose must not invent required paths: %#v", unspecified.RequiredTargets)
	}
	if len(unspecified.AuthorizedNewFiles) != 0 {
		t.Fatalf("unspecified new-file prose must not authorize paths: %#v", unspecified.AuthorizedNewFiles)
	}
	if !unspecified.ClosedScope {
		t.Fatalf("closed-scope instruction should still be preserved for unspecified new-file prose")
	}
}

func TestRunImplementationViaPlanExecuteKeepsReferenceMarkdownReadOnly(t *testing.T) {
	t.Setenv("IDIOM_PLAN_EXECUTE_DETERMINISTIC_CLEAN_REVIEW", "true")

	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	writeScopeIntakeFile(t, root, "docs/reference.md", "reference evidence stays unchanged\n")
	writeScopeIntakeFile(t, root, "docs/target.md", "old target\n")

	cfg := config.Default()
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()

	model.client = &scriptedLLMClient{responses: []string{
		`{"tool":"read_file","path":"docs/target.md","named_gap":"confirm current target markdown before submitting the scoped edit","why_not_answering":"required_edit_paths need source-body evidence before submit_plan"}`,
		`{"tool":"submit_plan","rationale":"Update only the requested target markdown file.","edits":[{"path":"docs/target.md","old_text":"old target","new_text":"new target","purpose":"Update the explicit mutation target"}]}`,
		"Decision: pass\n\nVerification Checklist:\n- Scope safety: Only docs/target.md changed.\n",
	}}

	prompt := "Use docs/reference.md as evidence before updating docs/target.md to say new target. Do not edit any other files."
	answer, err := model.runImplementationViaPlanExecute(context.Background(), prompt, scopeIntakePlanExecutePlan())
	if err != nil {
		t.Fatalf("runImplementationViaPlanExecute: %v\nanswer: %s", err, answer)
	}
	if strings.Contains(answer, "blocked before the planner ran") || strings.Contains(answer, "unresolved referenced design document") {
		t.Fatalf("reference markdown must not be treated as a blocking plan document, answer=%q", answer)
	}

	target, err := os.ReadFile(filepath.Join(root, "docs", "target.md"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(target) != "new target\n" {
		t.Fatalf("target content = %q, want new target", string(target))
	}

	reference, err := os.ReadFile(filepath.Join(root, "docs", "reference.md"))
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	if string(reference) != "reference evidence stays unchanged\n" {
		t.Fatalf("reference markdown should stay read-only, got %q", string(reference))
	}
}

func TestPlanExecuteScopeExpansionEventFormattingUsesLifecycleNames(t *testing.T) {
	got := implementationDispatchFormatScopeExpansionEvent(plancontract.ScopeExpansionEvent{
		Kind:   plancontract.ScopeExpansionEventProposalApproved,
		Policy: plancontract.ScopeExpansionPolicyBoundedDiscovery,
		Source: "harness_bounded_discovery",
		Reason: "candidate scope approved",
		Paths: []plancontract.ScopeExpansionPath{{
			Path:   "pkg/discovered.go",
			Role:   plancontract.ScopePathRoleCandidateExpansion,
			Source: "context_pack",
		}},
	})

	for _, want := range []string{
		"proposal_approved",
		"policy=bounded_discovery",
		"source=harness_bounded_discovery",
		"candidate_expansion:pkg/discovered.go",
		"candidate scope approved",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted event missing %q: %s", want, got)
		}
	}
}

func scopeIntakePlanExecutePlan() promptExecutionPlan {
	return promptExecutionPlan{
		route: turnRoutePlan{
			Family: "implementation",
			Reason: "typed_plan_execute_command",
		},
	}
}

func writeScopeIntakeFile(t *testing.T, root string, rel string, content string) {
	t.Helper()

	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}

	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
