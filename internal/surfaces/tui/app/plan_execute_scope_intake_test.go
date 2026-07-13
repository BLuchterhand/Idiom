package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/repository/planartifactstore"
	"idiom/internal/workflows/planexecute"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

const scopeIntakeContinuationPrompt = "please begin on your implementation"

var _ = ginkgo.It("plan execute scope intake classifies mutation targets as required closed scope", func() {
	prompt := "Please create docs/foo.md, update docs/bar.md, and append docs/changelog.md. Do not edit any other files."

	intake := implementationDispatchCompilePlanExecuteScopeIntake(prompt, scopeIntakePlanExecutePlan(), config.Config{})

	for _, want := range []string{"docs/foo.md", "docs/bar.md", "docs/changelog.md"} {
		gomega.Expect(slices.Contains(intake.RequiredTargets, want)).To(gomega.BeTrue(), fmt.Sprintf("required mutation targets missing %q: %#v", want, intake.RequiredTargets))
	}

	gomega.Expect(intake.ClosedScope).To(gomega.BeTrue(), fmt.Sprintf("closed scope was not detected"))
})

var _ = ginkgo.It("plan execute scope intake does not require reference or evidence paths", func() {
	prompt := "Use docs/reference.md as evidence and read docs/context.md before updating docs/target.md."

	intake := implementationDispatchCompilePlanExecuteScopeIntake(prompt, scopeIntakePlanExecutePlan(), config.Config{})

	gomega.Expect(slices.Contains(intake.RequiredTargets, "docs/target.md")).To(gomega.BeTrue(), fmt.Sprintf("mutation target missing from required targets: %#v", intake.RequiredTargets))

	for _, notWant := range []string{"docs/reference.md", "docs/context.md"} {
		gomega.Expect(slices.Contains(intake.RequiredTargets, notWant)).To(gomega.BeFalse(), fmt.Sprintf("reference/evidence path %q must not be required: %#v", notWant, intake.RequiredTargets))
	}
})

var _ = ginkgo.It("plan execute scope intake mutation markdown skips plan document resolution", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeScopeIntakeFile(t, root, "docs/foo.md", "# Existing Plan\n\n## Files to Modify\n\n- pkg/other.go\n")

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	prompt := "create/update/append docs/foo.md. Do not edit any other files."
	required, allowed, docs, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, scopeIntakePlanExecutePlan(), cfg)

	gomega.Expect(slices.Contains(required, "docs/foo.md")).To(gomega.BeTrue(), fmt.Sprintf("mutation markdown target missing from required paths: required=%#v allowed=%#v", required, allowed))

	gomega.Expect(len(docs)).To(gomega.Equal(0), fmt.Sprintf("mutation markdown target must not be resolved as a plan document: %#v", docs))

	gomega.Expect(len(blockers)).To(gomega.Equal(0), fmt.Sprintf("mutation markdown target must not produce plan-document blockers: %#v", blockers))
})

var _ = ginkgo.It("plan execute scope intake reference markdown does not become required", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeScopeIntakeFile(t, root, "docs/reference.md", "# Reference\n\nUse this as background.\n")
	writeScopeIntakeFile(t, root, "docs/target.md", "# Target\n")

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	prompt := "Use docs/reference.md as evidence before updating docs/target.md. Do not edit any other files."
	required, _, _, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, scopeIntakePlanExecutePlan(), cfg)

	gomega.Expect(slices.Contains(required, "docs/target.md")).To(gomega.BeTrue(), fmt.Sprintf("mutation target missing from required paths: %#v", required))

	gomega.Expect(slices.Contains(required, "docs/reference.md")).To(gomega.BeFalse(), fmt.Sprintf("reference markdown path must not become required: %#v", required))

	gomega.Expect(len(blockers)).To(gomega.Equal(0), fmt.Sprintf("reference markdown should not block scope intake: %#v", blockers))
})

var _ = ginkgo.It("plan execute scope intake authorizes only typed runtime brief new file paths", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	explicit := implementationDispatchCompilePlanExecuteScopeIntake(
		"Create new file docs/new-note.md with the smoke result. Do not edit any other files.",
		scopeIntakePlanExecutePlan(),
		cfg,
	)
	gomega.Expect(slices.Contains(explicit.RequiredTargets, "docs/new-note.md")).To(gomega.BeTrue(), fmt.Sprintf("explicit new file path missing from required targets: %#v", explicit.RequiredTargets))
	gomega.Expect(len(explicit.AuthorizedNewFiles)).To(gomega.Equal(0), fmt.Sprintf("prompt prose must not authorize new files: %#v", explicit.AuthorizedNewFiles))

	typed := scopeIntakePlanExecutePlan()
	typed.runtimeBrief.AuthorizedNewFiles = []string{"docs/new-note.md"}
	authorized := implementationDispatchCompilePlanExecuteScopeIntake(
		"Create new file docs/new-note.md with the smoke result. Do not edit any other files.",
		typed,
		cfg,
	)
	gomega.Expect(slices.Contains(authorized.AuthorizedNewFiles, "docs/new-note.md")).To(gomega.BeTrue(), fmt.Sprintf("typed runtime brief new file path missing from authorized new files: %#v", authorized.AuthorizedNewFiles))

	unspecified := implementationDispatchCompilePlanExecuteScopeIntake(
		"Create a new markdown note with the smoke result. Do not edit any other files.",
		scopeIntakePlanExecutePlan(),
		cfg,
	)
	gomega.Expect(len(unspecified.RequiredTargets)).To(gomega.Equal(0), fmt.Sprintf("unspecified new-file prose must not invent required paths: %#v", unspecified.RequiredTargets))
	gomega.Expect(len(unspecified.AuthorizedNewFiles)).To(gomega.Equal(0), fmt.Sprintf("unspecified new-file prose must not authorize paths: %#v", unspecified.AuthorizedNewFiles))
	gomega.Expect(unspecified.ClosedScope).To(gomega.BeTrue(), fmt.Sprintf("closed-scope instruction should still be preserved for unspecified new-file prose"))
})

var _ = ginkgo.It("persisted plan execute scope for continuation recovers approved plan paths", func() {
	t := ginkgo.GinkgoT()
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

	session := newDetachedPromptExecutionSession(context.Background(), &Model{cfg: cfg})
	scope, ok := session.persistedPlanExecuteScopeForContinuation(scopeIntakeContinuationPrompt)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected continuation prompt to recover latest approved plan scope"))
	gomega.Expect(slices.Equal(scope.RequiredEditPaths, []string{"docs/new-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("required paths = %#v, want docs/new-note.md", scope.RequiredEditPaths))
	gomega.Expect(slices.Equal(scope.AuthorizedNewFiles, []string{"docs/new-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("authorized new files = %#v, want docs/new-note.md", scope.AuthorizedNewFiles))
	gomega.Expect(filepath.Base(scope.PlanPath)).NotTo(gomega.Equal("last.plan.json"), fmt.Sprintf("scope must recover stamped plan artifact, got latest pointer %q", scope.PlanPath))
	gomega.Expect(scope.StatePath).NotTo(gomega.Equal(""), fmt.Sprintf("scope should identify the progress state it recovered from"))
})

var _ = ginkgo.It("persisted plan execute scope for continuation does not select unrelated latest session", func() {
	t := ginkgo.GinkgoT()
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

	session := newDetachedPromptExecutionSession(context.Background(), &Model{cfg: cfg})
	scope, ok := session.persistedPlanExecuteScopeForContinuation(scopeIntakeContinuationPrompt)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected continuation prompt to recover matching approved plan scope"))
	gomega.Expect(scope.SessionID).To(gomega.Equal("scope-intake-test-session"), fmt.Sprintf("selected session = %q, want matching scope-intake-test-session", scope.SessionID))
	gomega.Expect(slices.Equal(scope.RequiredEditPaths, []string{"docs/current-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("required paths = %#v, want docs/current-note.md", scope.RequiredEditPaths))
})

var _ = ginkgo.It("persisted plan execute scope for continuation ignores blocked plan artifact", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	planPath := writeScopeIntakeApprovedProgressPlanArtifact(t, root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}})
	body, err := os.ReadFile(planPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan artifact: %v", err))

	var artifact planartifact.PlanArtifact
	{
		err := json.Unmarshal(body, &artifact)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode plan artifact: %v", err))
	}
	artifact.Handoff.Ready = false
	artifact.Handoff.Status = planartifact.HandoffStatusBlocked
	artifact.Handoff.Blockers = []string{"operator_reapproval_required"}

	updated, err := json.MarshalIndent(artifact, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encode blocked plan artifact: %v", err))
	{
		err := os.WriteFile(planPath, append(updated, '\n'), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write blocked plan artifact: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	session := newDetachedPromptExecutionSession(context.Background(), &Model{cfg: cfg})
	{
		scope, ok := session.persistedPlanExecuteScopeForContinuation(scopeIntakeContinuationPrompt)
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("blocked plan artifact must not recover continuation scope: %#v", scope))
	}
})

var _ = ginkgo.It("persisted plan execute scope for continuation does not authorize implicit new file", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	planPath := writeScopeIntakeApprovedProgressPlanArtifact(t, root, []planartifact.PlanEdit{{
		Path:       "docs/new-note.md",
		NewText:    "new note\n",
		ReplaceAll: true,
		Purpose:    "Create the planned note",
	}})
	body, err := os.ReadFile(planPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan artifact: %v", err))

	var artifact planartifact.PlanArtifact
	{
		err := json.Unmarshal(body, &artifact)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode plan artifact: %v", err))
	}
	artifact.Approval.Required = false
	artifact.Approval.Status = planartifact.ApprovalStatusImplicit
	artifact.Approval.ApprovedPaths = nil
	artifact.Approval.ExecutionPermission = planartifact.ExecutionPermissionExecuteAll

	updated, err := json.MarshalIndent(artifact, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encode implicit plan artifact: %v", err))
	{
		err := os.WriteFile(planPath, append(updated, '\n'), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write implicit plan artifact: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	session := newDetachedPromptExecutionSession(context.Background(), &Model{cfg: cfg})
	scope, ok := session.persistedPlanExecuteScopeForContinuation(scopeIntakeContinuationPrompt)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("implicit executable plan should still recover continuation scope"))
	gomega.Expect(slices.Equal(scope.RequiredEditPaths, []string{"docs/new-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("required paths = %#v, want docs/new-note.md", scope.RequiredEditPaths))
	gomega.Expect(len(scope.AuthorizedNewFiles)).To(gomega.Equal(0), fmt.Sprintf("implicit approval must not grant new-file authority: %#v", scope.AuthorizedNewFiles))
})

var _ = ginkgo.It("run implementation via plan execute continuation executes stamped plan without planner", func() {
	t := ginkgo.GinkgoT()
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
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan artifact: %v", err))
	var artifact planartifact.PlanArtifact
	{
		err := json.Unmarshal(body, &artifact)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode plan artifact: %v", err))
	}
	artifact.Approval.Required = false
	artifact.Approval.Status = planartifact.ApprovalStatusImplicit
	artifact.Approval.ApprovedPaths = nil
	artifact.Approval.ExecutionPermission = planartifact.ExecutionPermissionExecuteAll
	updated, err := json.MarshalIndent(artifact, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encode implicit plan artifact: %v", err))
	{
		err := os.WriteFile(planPath, append(updated, '\n'), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write implicit plan artifact: %v", err))
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

	client := &scriptedLLMRecorder{responses: []string{
		`{"tool":"submit_plan","rationale":"malformed continuation should never ask the planner",}`,
	}, assertCallCount: true, expectedCallCount: 0}
	model.client = client.Mock(t)
	session := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := session.runImplementationViaPlanExecute(context.Background(), scopeIntakeContinuationPrompt, scopeIntakePlanExecutePlan())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runImplementationViaPlanExecute: %v\nanswer: %s", err, answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("concrete edit scope")), fmt.Sprintf("continuation should not surface no-scope or new-file authorization blocker:\n%s", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("missing edit path")), fmt.Sprintf("continuation should not surface no-scope or new-file authorization blocker:\n%s", answer))

	got, err := os.ReadFile(filepath.Join(root, "docs", "new-note.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read created note: %v\nanswer: %s", err, answer))
	gomega.Expect(string(got)).To(gomega.Equal("planned note\n"), fmt.Sprintf("created note = %q, want planned note", string(got)))

	gomega.Expect(session.lastPlanExecuteRunResult).NotTo(gomega.BeNil(), fmt.Sprintf("plan-execute result was not recorded"))
	state, _, found, err := planexecute.TryLoadProgressState(root, session.lastPlanExecuteRunResult.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load saved progress state: found=%t err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load saved progress state: found=%t err=%v", found, err))
	gomega.Expect(slices.Equal(state.RequiredEditPaths, []string{"docs/new-note.md"})).To(gomega.BeTrue(), fmt.Sprintf("saved required edit paths = %#v, want recovered docs/new-note.md", state.RequiredEditPaths))
	gomega.Expect(session.lastPlanExecuteRunResult.SessionID).To(gomega.Equal("scope-intake-test-session"), fmt.Sprintf("continuation should reuse recovered session, got %q", session.lastPlanExecuteRunResult.SessionID))
	gomega.Expect(client.callCount).To(gomega.Equal(0), fmt.Sprintf("continuation should execute stamped plan without planner calls, got %d", client.callCount))
})

var _ = ginkgo.It("run implementation via plan execute keeps reference markdown read only", func() {
	t := ginkgo.GinkgoT()
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

	model.client = (&scriptedLLMRecorder{
		responses: []string{
			`{"tool":"read_file","path":"docs/target.md","named_gap":"confirm current target markdown before submitting the scoped edit","why_not_answering":"required_edit_paths need source-body evidence before submit_plan"}`,
			`{"tool":"submit_plan","rationale":"Update only the requested target markdown file.","edits":[{"path":"docs/target.md","old_text":"old target","new_text":"new target","purpose":"Update the explicit mutation target"}]}`,
			"Decision: pass\n\nVerification Checklist:\n- Scope safety: Only docs/target.md changed.\n",
		},
		assertCallCount:   true,
		expectedCallCount: 3,
	}).Mock(t)
	session := newDetachedPromptExecutionSession(context.Background(), model)

	prompt := "Use docs/reference.md as evidence before updating docs/target.md to say new target. Do not edit any other files."
	answer, err := session.runImplementationViaPlanExecute(context.Background(), prompt, scopeIntakePlanExecutePlan())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runImplementationViaPlanExecute: %v\nanswer: %s", err, answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("blocked before the planner ran")), fmt.Sprintf("reference markdown must not be treated as a blocking plan document, answer=%q", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("unresolved referenced design document")), fmt.Sprintf("reference markdown must not be treated as a blocking plan document, answer=%q", answer))

	target, err := os.ReadFile(filepath.Join(root, "docs", "target.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read target: %v", err))
	gomega.Expect(string(target)).To(gomega.Equal("new target\n"), fmt.Sprintf("target content = %q, want new target", string(target)))

	reference, err := os.ReadFile(filepath.Join(root, "docs", "reference.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read reference: %v", err))
	gomega.Expect(string(reference)).To(gomega.Equal("reference evidence stays unchanged\n"), fmt.Sprintf("reference markdown should stay read-only, got %q", string(reference)))
})

var _ = ginkgo.It("plan execute scope expansion event formatting uses lifecycle names", func() {
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
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("formatted event missing %q: %s", want, got))
	}
})

func scopeIntakePlanExecutePlan() promptExecutionPlan {
	return promptExecutionPlan{
		route: turnRoutePlan{
			Family: "implementation",
			Reason: "typed_plan_execute_command",
		},
	}
}

func writeScopeIntakeFile(t ginkgo.FullGinkgoTInterface, root string, rel string, content string) {
	ginkgo.GinkgoHelper()

	abs := filepath.Join(root, filepath.FromSlash(rel))
	{
		err := os.MkdirAll(filepath.Dir(abs), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", rel, err))
	}

	{
		err := os.WriteFile(abs, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", rel, err))
	}
}

func writeScopeIntakeApprovedProgressPlanArtifact(t ginkgo.FullGinkgoTInterface, root string, edits []planartifact.PlanEdit) string {
	ginkgo.GinkgoHelper()
	return writeScopeIntakeApprovedProgressPlanArtifactWithOptions(t, root, "20260628-180000000.plan.json", "scope-intake-test", "plan_scope_intake_test", "scope-intake-test-session", scopeIntakeContinuationPrompt, time.Date(2026, 6, 28, 18, 0, 0, 0, time.UTC), edits)
}

func writeScopeIntakeApprovedProgressPlanArtifactWithOptions(t ginkgo.FullGinkgoTInterface, root string, stamp string, artifactSessionID string, planID string, progressSessionID string, prompt string, now time.Time, edits []planartifact.PlanEdit) string {
	ginkgo.GinkgoHelper()

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
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal plan artifact: %v", err))

	planPath, _, err := planartifactstore.StoreFilePath(root, stamp)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resolve plan artifact path: %v", err))
	{
		err := os.MkdirAll(filepath.Dir(planPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir plan artifact dir: %v", err))
	}
	{
		err := os.WriteFile(planPath, append(body, '\n'), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan artifact: %v", err))
	}

	state := planexecute.NewProgressState(progressSessionID, prompt, "", nil, nil, now)
	state.MarkPlanLoaded(root, planPath, artifact, planexecute.ReviewRepairBaseline{})
	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth
	state.FailureReason = "plan would create missing edit path without new-file authorization"
	statePath, err := planexecute.SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write progress state: %v", err))
	{
		err := os.Chtimes(filepath.Join(root, filepath.FromSlash(statePath)), now, now)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("set progress state mtime: %v", err))
	}

	return planPath
}
