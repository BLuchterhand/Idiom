package planexecute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/repository/planartifactstore"
)

func TestLoadApprovedContinuationScopeUsesProgressPlanAndApprovedNewFileProjection(t *testing.T) {
	root := t.TempDir()
	planPath := writeContinuationScopePlanArtifact(t, root, planartifact.PlanArtifactApproval{
		Required:            true,
		Status:              planartifact.ApprovalStatusApproved,
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ApprovedBy:          "operator",
		ApprovedAt:          "2026-06-28T18:00:01Z",
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ApprovedPaths:       []string{"docs/new-note.md"},
		ExecutionPermission: planartifact.ExecutionPermissionExecuteApprovedScope,
	})
	writeContinuationScopeProgressState(t, root, planPath)

	scope, ok, err := LoadApprovedContinuationScope(root, ContinuationScopeRequest{
		Prompt: "original prompt",
	}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	if err != nil || !ok {
		t.Fatalf("LoadApprovedContinuationScope ok=%t err=%v", ok, err)
	}
	if filepath.Base(scope.PlanPath) == "last.plan.json" {
		t.Fatalf("scope must use stamped progress plan path, got %q", scope.PlanPath)
	}
	if !slices.Equal(scope.RequiredEditPaths, []string{"docs/new-note.md"}) {
		t.Fatalf("required edit paths = %#v, want docs/new-note.md", scope.RequiredEditPaths)
	}
	if !slices.Equal(scope.AuthorizedNewFiles, []string{"docs/new-note.md"}) {
		t.Fatalf("authorized new files = %#v, want docs/new-note.md", scope.AuthorizedNewFiles)
	}
}

func TestLoadApprovedContinuationScopeDoesNotAuthorizeImplicitNewFile(t *testing.T) {
	root := t.TempDir()
	planPath := writeContinuationScopePlanArtifact(t, root, planartifact.PlanArtifactApproval{
		Required:            false,
		Status:              planartifact.ApprovalStatusImplicit,
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
	})
	writeContinuationScopeProgressState(t, root, planPath)

	scope, ok, err := LoadApprovedContinuationScope(root, ContinuationScopeRequest{
		Prompt: "original prompt",
	}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	if err != nil || !ok {
		t.Fatalf("LoadApprovedContinuationScope ok=%t err=%v", ok, err)
	}
	if !slices.Equal(scope.RequiredEditPaths, []string{"docs/new-note.md"}) {
		t.Fatalf("required edit paths = %#v, want docs/new-note.md", scope.RequiredEditPaths)
	}
	if len(scope.AuthorizedNewFiles) != 0 {
		t.Fatalf("implicit approval must not grant new-file authority: %#v", scope.AuthorizedNewFiles)
	}
}

func TestMissingNewFileAuthorizationPathsUsesPlanPreflightProjection(t *testing.T) {
	root := t.TempDir()
	planPath := writeContinuationScopePlanArtifact(t, root, planartifact.PlanArtifactApproval{
		Required:            false,
		Status:              planartifact.ApprovalStatusImplicit,
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
	})
	writeContinuationScopeProgressState(t, root, planPath)

	state, _, found, err := TryLoadProgressState(root, "continuation-scope-test-session")
	if err != nil || !found {
		t.Fatalf("load progress state: found=%t err=%v", found, err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root

	missing, err := MissingNewFileAuthorizationPaths(cfg, state, nil, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	if err != nil {
		t.Fatalf("MissingNewFileAuthorizationPaths: %v", err)
	}
	if !slices.Equal(missing, []string{"docs/new-note.md"}) {
		t.Fatalf("missing new-file auth paths = %#v, want docs/new-note.md", missing)
	}

	authorized, err := MissingNewFileAuthorizationPaths(cfg, state, []string{"docs/new-note.md"}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	if err != nil {
		t.Fatalf("MissingNewFileAuthorizationPaths authorized: %v", err)
	}
	if len(authorized) != 0 {
		t.Fatalf("explicit authorized new file should clear missing paths: %#v", authorized)
	}
}

func TestLoadApprovedContinuationScopeRequiresContextForImplicitLatest(t *testing.T) {
	root := t.TempDir()
	approval := planartifact.PlanArtifactApproval{
		Required:            true,
		Status:              planartifact.ApprovalStatusApproved,
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ApprovedBy:          "operator",
		ApprovedAt:          "2026-06-28T18:00:01Z",
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ApprovedPaths:       []string{"docs/current-note.md"},
		ExecutionPermission: planartifact.ExecutionPermissionExecuteApprovedScope,
	}
	currentPlanPath := writeContinuationScopePlanArtifactForPath(t, root, "20260628-180000000.plan.json", "current-note", "docs/current-note.md", approval)
	currentStatePath := writeContinuationScopeProgressStateForSession(t, root, "current-session", "approve current task", currentPlanPath, time.Date(2026, 6, 28, 18, 0, 0, 0, time.UTC))

	staleApproval := approval
	staleApproval.ApprovedPaths = []string{"docs/stale-note.md"}
	stalePlanPath := writeContinuationScopePlanArtifactForPath(t, root, "20260628-181000000.plan.json", "stale-note", "docs/stale-note.md", staleApproval)
	staleStatePath := writeContinuationScopeProgressStateForSession(t, root, "stale-session", "approve unrelated stale task", stalePlanPath, time.Date(2026, 6, 28, 18, 10, 0, 0, time.UTC))

	legacyApproval := approval
	legacyApproval.ApprovedPaths = []string{"docs/legacy-note.md"}
	legacyPlanPath := writeContinuationScopePlanArtifactForPath(t, root, "20260628-182000000.plan.json", "legacy-note", "docs/legacy-note.md", legacyApproval)
	writeContinuationScopeProgressStateForSessionWithoutPromptHash(t, root, "legacy-session", "approve unrelated legacy task", legacyPlanPath, time.Date(2026, 6, 28, 18, 20, 0, 0, time.UTC))

	scope, ok, err := LoadApprovedContinuationScope(root, ContinuationScopeRequest{
		Prompt: "approve current task",
	}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	if err != nil || !ok {
		t.Fatalf("LoadApprovedContinuationScope matched prompt ok=%t err=%v", ok, err)
	}
	if scope.SessionID != "current-session" {
		t.Fatalf("selected session = %q, want current-session; stale state path was %s", scope.SessionID, staleStatePath)
	}
	if scope.StatePath != currentStatePath {
		t.Fatalf("state path = %q, want %q", scope.StatePath, currentStatePath)
	}
	if !slices.Equal(scope.RequiredEditPaths, []string{"docs/current-note.md"}) {
		t.Fatalf("required edit paths = %#v, want docs/current-note.md", scope.RequiredEditPaths)
	}

	if scope, ok, err := LoadApprovedContinuationScope(root, ContinuationScopeRequest{}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact}); err != nil || ok {
		t.Fatalf("context-free implicit lookup must not select latest stale continuation: ok=%t err=%v scope=%#v", ok, err, scope)
	}
}

func TestRunExecutorContinuationExecutesStampedPlanWithoutPromptRunner(t *testing.T) {
	root := t.TempDir()
	assetPath := filepath.Join(root, ".idiom", "plans", "assets", "continuation-body.txt")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatalf("mkdir asset dir: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("hydrated planned note\n"), 0o644); err != nil {
		t.Fatalf("write body ref asset: %v", err)
	}

	planPath := writeContinuationScopePlanArtifactWithEdits(t, root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewTextRef: "assets/continuation-body.txt",
		ReplaceAll: true,
		Purpose:    "Create the planned note from a body ref",
	}}, planartifact.PlanArtifactApproval{
		Required:            true,
		Status:              planartifact.ApprovalStatusApproved,
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ApprovedBy:          "operator",
		ApprovedAt:          "2026-06-28T18:00:01Z",
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ApprovedPaths:       []string{"docs/new-note.md"},
		ExecutionPermission: planartifact.ExecutionPermissionExecuteApprovedScope,
	})
	writeContinuationScopeProgressState(t, root, planPath)

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false

	var stdout, stderr bytes.Buffer
	deps := planExecuteTestDependencies()
	deps.LoadPlanArtifact = workspace.LoadPlanArtifact
	result := RunExecutorContinuationWithDependencies(cfg, Options{
		Context:          context.Background(),
		Prompt:           "please begin on your implementation",
		Resume:           "continuation-scope-test-session",
		SkipReviewRepair: true,
	}, func(config.Config, string, RunOptions, io.Writer) (string, error) {
		t.Fatal("continuation execution must not call the planner prompt runner")
		return "", fmt.Errorf("unexpected prompt runner call")
	}, nil, &stdout, &stderr, deps)

	if result.ExitCode != 0 {
		t.Fatalf("RunExecutorContinuationWithDependencies exit=%d result=%#v stdout=%s stderr=%s", result.ExitCode, result, stdout.String(), stderr.String())
	}

	got, err := os.ReadFile(filepath.Join(root, "docs", "new-note.md"))
	if err != nil {
		t.Fatalf("read created note: %v", err)
	}
	if string(got) != "hydrated planned note\n" {
		t.Fatalf("created note = %q, want hydrated body ref content", string(got))
	}
	if result.SessionID != "continuation-scope-test-session" {
		t.Fatalf("session id = %q, want recovered continuation session", result.SessionID)
	}
}

func writeContinuationScopePlanArtifact(t *testing.T, root string, approval planartifact.PlanArtifactApproval) string {
	t.Helper()

	return writeContinuationScopePlanArtifactWithEdits(t, root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}}, approval)
}

func writeContinuationScopePlanArtifactWithEdits(t *testing.T, root string, edits []planartifact.PlanEdit, approval planartifact.PlanArtifactApproval) string {
	t.Helper()
	return writeContinuationScopePlanArtifactWithOptions(t, root, "20260628-180000000.plan.json", "continuation-scope-test", "plan_continuation_scope_test", edits, approval)
}

func writeContinuationScopePlanArtifactForPath(t *testing.T, root string, stamp string, id string, editPath string, approval planartifact.PlanArtifactApproval) string {
	t.Helper()

	return writeContinuationScopePlanArtifactWithOptions(t, root, stamp, id, "plan_"+id, []planartifact.PlanEdit{{
		Path:       editPath,
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}}, approval)
}

func writeContinuationScopePlanArtifactWithOptions(t *testing.T, root string, stamp string, sessionID string, planID string, edits []planartifact.PlanEdit, approval planartifact.PlanArtifactApproval) string {
	t.Helper()

	var paths []string
	for _, edit := range edits {
		paths = append(paths, edit.Path)
	}

	artifact := planartifact.NormalizeForSubmit(planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        planID,
		Created:       "2026-06-28T18:00:00Z",
		Rationale:     "Approved continuation fixture.",
		SessionID:     sessionID,
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
		Approval: approval,
		Handoff: planartifact.PlanArtifactHandoff{
			From:   planartifact.OwnerPlanner,
			To:     planartifact.HandoffTargetPlanExecuteExecutor,
			Status: planartifact.HandoffStatusReady,
			Ready:  true,
		},
	}, planartifact.NormalizeForSubmitOptions{
		SessionID: sessionID,
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

	return planPath
}

func writeContinuationScopeProgressState(t *testing.T, root string, planPath string) {
	t.Helper()
	writeContinuationScopeProgressStateForSession(t, root, "continuation-scope-test-session", "original prompt", planPath, time.Date(2026, 6, 28, 18, 0, 0, 0, time.UTC))
}

func writeContinuationScopeProgressStateForSession(t *testing.T, root string, sessionID string, prompt string, planPath string, now time.Time) string {
	t.Helper()
	return writeContinuationScopeProgressStateForSessionWithPromptHash(t, root, sessionID, prompt, planPath, now, true)
}

func writeContinuationScopeProgressStateForSessionWithoutPromptHash(t *testing.T, root string, sessionID string, prompt string, planPath string, now time.Time) string {
	t.Helper()
	return writeContinuationScopeProgressStateForSessionWithPromptHash(t, root, sessionID, prompt, planPath, now, false)
}

func writeContinuationScopeProgressStateForSessionWithPromptHash(t *testing.T, root string, sessionID string, prompt string, planPath string, now time.Time, keepPromptHash bool) string {
	t.Helper()

	artifact, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		t.Fatalf("load plan artifact: %v", err)
	}

	state := NewProgressState(sessionID, prompt, "", nil, nil, now)
	if !keepPromptHash {
		state.PromptSHA256 = ""
	}
	state.MarkPlanLoaded(root, planPath, artifact, ReviewRepairBaseline{})
	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth
	state.FailureReason = "plan would create missing edit path without new-file authorization"
	statePath, err := SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("write progress state: %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(statePath)), now, now); err != nil {
		t.Fatalf("set progress state mtime: %v", err)
	}

	return statePath
}
