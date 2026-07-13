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
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/repository/planartifactstore"
)

var _ = ginkgo.It("load approved continuation scope uses progress plan and approved new file projection", func() {
	root := ginkgo.GinkgoT().TempDir()
	planPath := writeContinuationScopePlanArtifact(root, planartifact.PlanArtifactApproval{
		Required:            true,
		Status:              planartifact.ApprovalStatusApproved,
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ApprovedBy:          "operator",
		ApprovedAt:          "2026-06-28T18:00:01Z",
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ApprovedPaths:       []string{"docs/new-note.md"},
		ExecutionPermission: planartifact.ExecutionPermissionExecuteApprovedScope,
	})
	writeContinuationScopeProgressState(root, planPath)

	scope, ok, err := LoadApprovedContinuationScope(root, ContinuationScopeRequest{
		Prompt: "original prompt",
	}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("LoadApprovedContinuationScope ok=%t err=%v", ok, err))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("LoadApprovedContinuationScope ok=%t err=%v", ok, err))
	gomega.Expect(filepath.Base(scope.PlanPath) == "last.plan.json").To(gomega.BeFalse(), fmt.Sprintf("scope must use stamped progress plan path, got %q", scope.PlanPath))
	gomega.Expect(slices.Equal(scope.RequiredEditPaths, []string{"docs/new-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("required edit paths = %#v, want docs/new-note.md", scope.RequiredEditPaths))
	gomega.Expect(slices.Equal(scope.AuthorizedNewFiles, []string{"docs/new-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("authorized new files = %#v, want docs/new-note.md", scope.AuthorizedNewFiles))
})

var _ = ginkgo.It("load approved continuation scope does not authorize implicit new file", func() {
	root := ginkgo.GinkgoT().TempDir()
	planPath := writeContinuationScopePlanArtifact(root, planartifact.PlanArtifactApproval{
		Required:            false,
		Status:              planartifact.ApprovalStatusImplicit,
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
	})
	writeContinuationScopeProgressState(root, planPath)

	scope, ok, err := LoadApprovedContinuationScope(root, ContinuationScopeRequest{
		Prompt: "original prompt",
	}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("LoadApprovedContinuationScope ok=%t err=%v", ok, err))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("LoadApprovedContinuationScope ok=%t err=%v", ok, err))
	gomega.Expect(slices.Equal(scope.RequiredEditPaths, []string{"docs/new-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("required edit paths = %#v, want docs/new-note.md", scope.RequiredEditPaths))
	gomega.Expect(len(scope.AuthorizedNewFiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("implicit approval must not grant new-file authority: %#v", scope.AuthorizedNewFiles))
})

var _ = ginkgo.It("missing new file authorization paths uses plan preflight projection", func() {
	root := ginkgo.GinkgoT().TempDir()
	planPath := writeContinuationScopePlanArtifact(root, planartifact.PlanArtifactApproval{
		Required:            false,
		Status:              planartifact.ApprovalStatusImplicit,
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
	})
	writeContinuationScopeProgressState(root, planPath)

	state, _, found, err := TryLoadProgressState(root, "continuation-scope-test-session")
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: found=%t err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: found=%t err=%v", found, err))

	cfg := config.Default()
	cfg.Workspace.Root = root

	missing, err := MissingNewFileAuthorizationPaths(cfg, state, nil, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("MissingNewFileAuthorizationPaths: %v", err))
	gomega.Expect(slices.Equal(missing, []string{"docs/new-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("missing new-file auth paths = %#v, want docs/new-note.md", missing))

	authorized, err := MissingNewFileAuthorizationPaths(cfg, state, []string{"docs/new-note.md"}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("MissingNewFileAuthorizationPaths authorized: %v", err))
	gomega.Expect(len(authorized) != 0).To(gomega.BeFalse(), fmt.Sprintf("explicit authorized new file should clear missing paths: %#v", authorized))
})

var _ = ginkgo.It("load approved continuation scope requires context for implicit latest", func() {
	root := ginkgo.GinkgoT().TempDir()
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
	currentPlanPath := writeContinuationScopePlanArtifactForPath(root, "20260628-180000000.plan.json", "current-note", "docs/current-note.md", approval)
	currentStatePath := writeContinuationScopeProgressStateForSession(root, "current-session", "approve current task", currentPlanPath, time.Date(2026, 6, 28, 18, 0, 0, 0, time.UTC))

	staleApproval := approval
	staleApproval.ApprovedPaths = []string{"docs/stale-note.md"}
	stalePlanPath := writeContinuationScopePlanArtifactForPath(root, "20260628-181000000.plan.json", "stale-note", "docs/stale-note.md", staleApproval)
	staleStatePath := writeContinuationScopeProgressStateForSession(root, "stale-session", "approve unrelated stale task", stalePlanPath, time.Date(2026, 6, 28, 18, 10, 0, 0, time.UTC))

	legacyApproval := approval
	legacyApproval.ApprovedPaths = []string{"docs/legacy-note.md"}
	legacyPlanPath := writeContinuationScopePlanArtifactForPath(root, "20260628-182000000.plan.json", "legacy-note", "docs/legacy-note.md", legacyApproval)
	writeContinuationScopeProgressStateForSessionWithoutPromptHash(root, "legacy-session", "approve unrelated legacy task", legacyPlanPath, time.Date(2026, 6, 28, 18, 20, 0, 0, time.UTC))

	scope, ok, err := LoadApprovedContinuationScope(root, ContinuationScopeRequest{
		Prompt: "approve current task",
	}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact})
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("LoadApprovedContinuationScope matched prompt ok=%t err=%v", ok, err))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("LoadApprovedContinuationScope matched prompt ok=%t err=%v", ok, err))
	gomega.Expect(scope.SessionID != "current-session").To(gomega.BeFalse(), fmt.Sprintf("selected session = %q, want current-session; stale state path was %s", scope.SessionID, staleStatePath))
	gomega.Expect(scope.StatePath != currentStatePath).To(gomega.BeFalse(), fmt.Sprintf("state path = %q, want %q", scope.StatePath, currentStatePath))
	gomega.Expect(slices.Equal(scope.RequiredEditPaths, []string{"docs/current-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("required edit paths = %#v, want docs/current-note.md", scope.RequiredEditPaths))

	if scope, ok, err := LoadApprovedContinuationScope(root, ContinuationScopeRequest{}, Dependencies{LoadPlanArtifact: workspace.LoadPlanArtifact}); err != nil || ok {
		ginkgo.Fail(fmt.Sprintf("context-free implicit lookup must not select latest stale continuation: ok=%t err=%v scope=%#v", ok, err, scope))
	}
})

var _ = ginkgo.It("run executor continuation executes stamped plan without prompt runner", func() {
	root := ginkgo.GinkgoT().TempDir()
	assetPath := filepath.Join(root, ".idiom", "plans", "assets", "continuation-body.txt")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir asset dir: %v", err))
	}
	if err := os.WriteFile(assetPath, []byte("hydrated planned note\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write body ref asset: %v", err))
	}

	planPath := writeContinuationScopePlanArtifactWithEdits(root, []planartifact.PlanEdit{{
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
	writeContinuationScopeProgressState(root, planPath)

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
		ginkgo.Fail(fmt.Sprint("continuation execution must not call the planner prompt runner"))
		return "", fmt.Errorf("unexpected prompt runner call")
	}, nil, &stdout, &stderr, deps)
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("RunExecutorContinuationWithDependencies exit=%d result=%#v stdout=%s stderr=%s", result.ExitCode, result, stdout.String(), stderr.String()))

	got, err := os.ReadFile(filepath.Join(root, "docs", "new-note.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read created note: %v", err))
	gomega.Expect(string(got) != "hydrated planned note\n").To(gomega.BeFalse(), fmt.Sprintf("created note = %q, want hydrated body ref content", string(got)))
	gomega.Expect(result.SessionID != "continuation-scope-test-session").To(gomega.BeFalse(), fmt.Sprintf("session id = %q, want recovered continuation session", result.SessionID))
})

func writeContinuationScopePlanArtifact(root string, approval planartifact.PlanArtifactApproval) string {
	ginkgo.GinkgoHelper()

	return writeContinuationScopePlanArtifactWithEdits(root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}}, approval)
}

func writeContinuationScopePlanArtifactWithEdits(root string, edits []planartifact.PlanEdit, approval planartifact.PlanArtifactApproval) string {
	ginkgo.GinkgoHelper()

	return writeContinuationScopePlanArtifactWithOptions(root, "20260628-180000000.plan.json", "continuation-scope-test", "plan_continuation_scope_test", edits, approval)
}

func writeContinuationScopePlanArtifactForPath(root string, stamp string, id string, editPath string, approval planartifact.PlanArtifactApproval) string {
	ginkgo.GinkgoHelper()

	return writeContinuationScopePlanArtifactWithOptions(root, stamp, id, "plan_"+id, []planartifact.PlanEdit{{
		Path:       editPath,
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}}, approval)
}

func writeContinuationScopePlanArtifactWithOptions(root string, stamp string, sessionID string, planID string, edits []planartifact.PlanEdit, approval planartifact.PlanArtifactApproval) string {
	ginkgo.GinkgoHelper()

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
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal plan artifact: %v", err))

	planPath, _, err := planartifactstore.StoreFilePath(root, stamp)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resolve plan artifact path: %v", err))

	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir plan artifact dir: %v", err))
	}
	if err := os.WriteFile(planPath, append(body, '\n'), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write plan artifact: %v", err))
	}

	return planPath
}

func writeContinuationScopeProgressState(root string, planPath string) {
	ginkgo.GinkgoHelper()

	writeContinuationScopeProgressStateForSession(root, "continuation-scope-test-session", "original prompt", planPath, time.Date(2026, 6, 28, 18, 0, 0, 0, time.UTC))
}

func writeContinuationScopeProgressStateForSession(root string, sessionID string, prompt string, planPath string, now time.Time) string {
	ginkgo.GinkgoHelper()

	return writeContinuationScopeProgressStateForSessionWithPromptHash(root, sessionID, prompt, planPath, now, true)
}

func writeContinuationScopeProgressStateForSessionWithoutPromptHash(root string, sessionID string, prompt string, planPath string, now time.Time) string {
	ginkgo.GinkgoHelper()

	return writeContinuationScopeProgressStateForSessionWithPromptHash(root, sessionID, prompt, planPath, now, false)
}

func writeContinuationScopeProgressStateForSessionWithPromptHash(root string, sessionID string, prompt string, planPath string, now time.Time, keepPromptHash bool) string {
	ginkgo.GinkgoHelper()

	artifact, err := workspace.LoadPlanArtifact(planPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load plan artifact: %v", err))

	state := NewProgressState(sessionID, prompt, "", nil, nil, now)
	if !keepPromptHash {
		state.PromptSHA256 = ""
	}
	state.MarkPlanLoaded(root, planPath, artifact, ReviewRepairBaseline{})
	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth
	state.FailureReason = "plan would create missing edit path without new-file authorization"
	statePath, err := SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write progress state: %v", err))

	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(statePath)), now, now); err != nil {
		ginkgo.Fail(fmt.Sprintf("set progress state mtime: %v", err))
	}

	return statePath
}
