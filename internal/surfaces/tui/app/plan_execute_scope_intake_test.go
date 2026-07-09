package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/repository/planartifactstore"
	"idiom/internal/workflows/planexecute"
)

const scopeIntakeContinuationPrompt = "please begin on your implementation"

func TestPlanExecuteScopeIntakeClassifiesMutationTargetsAsRequiredClosedScope(t *testing.T) {
	prompt := "Please create docs/foo.md, update docs/bar.md, and append docs/changelog.md. Do not edit any other files."

	intake := implementationDispatchCompilePlanExecuteScopeIntake(prompt, scopeIntakePlanExecutePlan(), config.Config{})

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

	intake := implementationDispatchCompilePlanExecuteScopeIntake(prompt, scopeIntakePlanExecutePlan(), config.Config{})

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

func TestPersistedPlanExecuteScopeForContinuationRecoversApprovedPlanPaths(t *testing.T) {
	root := t.TempDir()
	writeScopeIntakeApprovedProgressPlanArtifact(t, root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}})

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := &Model{cfg: cfg}
	scope, ok := model.persistedPlanExecuteScopeForContinuation(scopeIntakeContinuationPrompt)
	if !ok {
		t.Fatal("expected continuation prompt to recover latest approved plan scope")
	}
	if !slices.Equal(scope.RequiredEditPaths, []string{"docs/new-note.md"}) {
		t.Fatalf("required paths = %#v, want docs/new-note.md", scope.RequiredEditPaths)
	}
	if !slices.Equal(scope.AuthorizedNewFiles, []string{"docs/new-note.md"}) {
		t.Fatalf("authorized new files = %#v, want docs/new-note.md", scope.AuthorizedNewFiles)
	}
	if filepath.Base(scope.PlanPath) == "last.plan.json" {
		t.Fatalf("scope must recover stamped plan artifact, got latest pointer %q", scope.PlanPath)
	}
	if scope.StatePath == "" {
		t.Fatalf("scope should identify the progress state it recovered from")
	}
}

func TestPersistedPlanExecuteScopeForContinuationDoesNotSelectUnrelatedLatestSession(t *testing.T) {
	root := t.TempDir()
	writeScopeIntakeApprovedProgressPlanArtifact(t, root, []planartifact.PlanEdit{{
		Path:       "docs/current-note.md",
		NewText:    "current note\n",
		ReplaceAll: true,
		Purpose:    "Create the current planned note",
	}})
	writeScopeIntakeApprovedProgressPlanArtifactWithOptions(t, root, "20260628-181000000.plan.json", "stale-scope-intake-test", "plan_stale_scope_intake_test", "stale-scope-intake-test-session", "approve unrelated stale task", time.Date(2026, 6, 28, 18, 10, 0, 0, time.UTC), []planartifact.PlanEdit{{
		Path:       "docs/stale-note.md",
		NewText:    "stale note\n",
		ReplaceAll: true,
		Purpose:    "Create the stale planned note",
	}})

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := &Model{cfg: cfg}
	scope, ok := model.persistedPlanExecuteScopeForContinuation(scopeIntakeContinuationPrompt)
	if !ok {
		t.Fatal("expected continuation prompt to recover matching approved plan scope")
	}
	if scope.SessionID != "scope-intake-test-session" {
		t.Fatalf("selected session = %q, want matching scope-intake-test-session", scope.SessionID)
	}
	if !slices.Equal(scope.RequiredEditPaths, []string{"docs/current-note.md"}) {
		t.Fatalf("required paths = %#v, want docs/current-note.md", scope.RequiredEditPaths)
	}
}

func TestPersistedPlanExecuteScopeForContinuationIgnoresBlockedPlanArtifact(t *testing.T) {
	root := t.TempDir()
	planPath := writeScopeIntakeApprovedProgressPlanArtifact(t, root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}})
	body, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan artifact: %v", err)
	}

	var artifact planartifact.PlanArtifact
	if err := json.Unmarshal(body, &artifact); err != nil {
		t.Fatalf("decode plan artifact: %v", err)
	}
	artifact.Handoff.Ready = false
	artifact.Handoff.Status = planartifact.HandoffStatusBlocked
	artifact.Handoff.Blockers = []string{"operator_reapproval_required"}

	updated, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("encode blocked plan artifact: %v", err)
	}
	if err := os.WriteFile(planPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatalf("write blocked plan artifact: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := &Model{cfg: cfg}
	if scope, ok := model.persistedPlanExecuteScopeForContinuation(scopeIntakeContinuationPrompt); ok {
		t.Fatalf("blocked plan artifact must not recover continuation scope: %#v", scope)
	}
}

func TestPersistedPlanExecuteScopeForContinuationDoesNotAuthorizeImplicitNewFile(t *testing.T) {
	root := t.TempDir()
	planPath := writeScopeIntakeApprovedProgressPlanArtifact(t, root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}})
	body, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan artifact: %v", err)
	}

	var artifact planartifact.PlanArtifact
	if err := json.Unmarshal(body, &artifact); err != nil {
		t.Fatalf("decode plan artifact: %v", err)
	}
	artifact.Approval.Required = false
	artifact.Approval.Status = planartifact.ApprovalStatusImplicit
	artifact.Approval.ApprovedPaths = nil
	artifact.Approval.ExecutionPermission = planartifact.ExecutionPermissionExecuteAll

	updated, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("encode implicit plan artifact: %v", err)
	}
	if err := os.WriteFile(planPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatalf("write implicit plan artifact: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := &Model{cfg: cfg}
	scope, ok := model.persistedPlanExecuteScopeForContinuation(scopeIntakeContinuationPrompt)
	if !ok {
		t.Fatal("implicit executable plan should still recover continuation scope")
	}
	if !slices.Equal(scope.RequiredEditPaths, []string{"docs/new-note.md"}) {
		t.Fatalf("required paths = %#v, want docs/new-note.md", scope.RequiredEditPaths)
	}
	if len(scope.AuthorizedNewFiles) != 0 {
		t.Fatalf("implicit approval must not grant new-file authority: %#v", scope.AuthorizedNewFiles)
	}
}

func TestRunImplementationViaPlanExecuteContinuationExecutesStampedPlanWithoutPlanner(t *testing.T) {
	t.Setenv("IDIOM_PLAN_EXECUTE_DETERMINISTIC_CLEAN_REVIEW", "true")

	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	planPath := writeScopeIntakeApprovedProgressPlanArtifact(t, root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewText:    "planned note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}})
	body, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan artifact: %v", err)
	}
	var artifact planartifact.PlanArtifact
	if err := json.Unmarshal(body, &artifact); err != nil {
		t.Fatalf("decode plan artifact: %v", err)
	}
	artifact.Approval.Required = false
	artifact.Approval.Status = planartifact.ApprovalStatusImplicit
	artifact.Approval.ApprovedPaths = nil
	artifact.Approval.ExecutionPermission = planartifact.ExecutionPermissionExecuteAll
	updated, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("encode implicit plan artifact: %v", err)
	}
	if err := os.WriteFile(planPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatalf("write implicit plan artifact: %v", err)
	}

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
	model.planExecuteApproveAllEdits = true

	client := &scriptedLLMClient{responses: []string{
		`{"tool":"submit_plan","rationale":"malformed continuation should never ask the planner",}`,
	}}
	model.client = client

	answer, err := model.runImplementationViaPlanExecute(context.Background(), scopeIntakeContinuationPrompt, scopeIntakePlanExecutePlan())
	if err != nil {
		t.Fatalf("runImplementationViaPlanExecute: %v\nanswer: %s", err, answer)
	}
	if strings.Contains(answer, "concrete edit scope") || strings.Contains(answer, "missing edit path") {
		t.Fatalf("continuation should not surface no-scope or new-file authorization blocker:\n%s", answer)
	}

	got, err := os.ReadFile(filepath.Join(root, "docs", "new-note.md"))
	if err != nil {
		t.Fatalf("read created note: %v\nanswer: %s", err, answer)
	}
	if string(got) != "planned note\n" {
		t.Fatalf("created note = %q, want planned note", string(got))
	}

	if model.lastPlanExecuteRunResult == nil {
		t.Fatalf("plan-execute result was not recorded")
	}
	state, _, found, err := planexecute.TryLoadProgressState(root, model.lastPlanExecuteRunResult.SessionID)
	if err != nil || !found {
		t.Fatalf("load saved progress state: found=%t err=%v", found, err)
	}
	if !slices.Equal(state.RequiredEditPaths, []string{"docs/new-note.md"}) {
		t.Fatalf("saved required edit paths = %#v, want recovered docs/new-note.md", state.RequiredEditPaths)
	}
	if model.lastPlanExecuteRunResult.SessionID != "scope-intake-test-session" {
		t.Fatalf("continuation should reuse recovered session, got %q", model.lastPlanExecuteRunResult.SessionID)
	}
	if client.callCount != 0 {
		t.Fatalf("continuation should execute stamped plan without planner calls, got %d", client.callCount)
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
	model.planExecuteApproveAllEdits = true

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

func writeScopeIntakeApprovedProgressPlanArtifact(t *testing.T, root string, edits []planartifact.PlanEdit) string {
	t.Helper()
	return writeScopeIntakeApprovedProgressPlanArtifactWithOptions(t, root, "20260628-180000000.plan.json", "scope-intake-test", "plan_scope_intake_test", "scope-intake-test-session", scopeIntakeContinuationPrompt, time.Date(2026, 6, 28, 18, 0, 0, 0, time.UTC), edits)
}

func writeScopeIntakeApprovedProgressPlanArtifactWithOptions(t *testing.T, root string, stamp string, artifactSessionID string, planID string, progressSessionID string, prompt string, now time.Time, edits []planartifact.PlanEdit) string {
	t.Helper()

	paths := make([]string, 0, len(edits))
	for _, edit := range edits {
		paths = append(paths, edit.Path)
	}

	artifact := planartifact.NormalizeForSubmit(planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        planID,
		Created:       "2026-06-28T18:00:00Z",
		Rationale:     "Approved continuation fixture.",
		SessionID:     artifactSessionID,
		Edits:         edits,
		Task: planartifact.PlanArtifactTask{
			Summary: "approved continuation fixture",
		},
		Contract: planartifact.PlanArtifactContract{
			Kind:              planartifact.ContractKindPlanThenExecute,
			StrategyOwner:     planartifact.OwnerModel,
			ContractOwner:     planartifact.OwnerHarness,
			PermissionProfile: planartifact.PermissionProfilePlanThenExecute,
			VerificationOwner: planartifact.OwnerPlanner,
			RequiredEditPaths: paths,
		},
		Packet: planartifact.PlanArtifactPacket{
			Status: planartifact.PacketStatusNotProvided,
		},
		Paths: planartifact.PlanArtifactPaths{
			Required: paths,
			Editable: paths,
			Changed:  paths,
		},
		VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     planartifact.VerificationKindExecutorLaneSanity,
			Owner:    planartifact.OwnerPlanner,
			Status:   planartifact.VerificationStatusOpen,
			Required: true,
		}},
		Approval: planartifact.PlanArtifactApproval{
			Required:            true,
			Status:              planartifact.ApprovalStatusApproved,
			Mode:                planartifact.ApprovalModePlanThenExecute,
			ApprovedBy:          "operator",
			ApprovedAt:          "2026-06-28T18:00:01Z",
			ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
			ApprovedPaths:       paths,
			ExecutionPermission: planartifact.ExecutionPermissionExecuteApprovedScope,
		},
		Handoff: planartifact.PlanArtifactHandoff{
			From:   planartifact.OwnerPlanner,
			To:     planartifact.HandoffTargetPlanExecuteExecutor,
			Status: planartifact.HandoffStatusReady,
			Ready:  true,
		},
	}, planartifact.NormalizeForSubmitOptions{
		SessionID: artifactSessionID,
		Created:   "2026-06-28T18:00:00Z",
		PlanID:    planID,
	})

	body, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan artifact: %v", err)
	}

	planPath, _, err := planartifactstore.StoreFilePath(root, stamp)
	if err != nil {
		t.Fatalf("resolve plan artifact path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("mkdir plan artifact dir: %v", err)
	}
	if err := os.WriteFile(planPath, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write plan artifact: %v", err)
	}

	state := planexecute.NewProgressState(progressSessionID, prompt, "", nil, nil, now)
	state.MarkPlanLoaded(root, planPath, artifact, planexecute.ReviewRepairBaseline{})
	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth
	state.FailureReason = "plan would create missing edit path without new-file authorization"
	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("write progress state: %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(statePath)), now, now); err != nil {
		t.Fatalf("set progress state mtime: %v", err)
	}

	return planPath
}
