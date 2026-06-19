package intentloop

import (
	"testing"

	"idiom/internal/workflows/planexecute"
)

func TestAssessResumeReloadsIncompleteCyclePromptAndChildProgress(t *testing.T) {
	root := t.TempDir()
	prompt := "cycle prompt"
	state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, prompt)
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}

	identity := testChildIdentity()
	state.Cycles = []CycleRecord{{
		CycleNumber:             1,
		Status:                  "dispatched",
		PlanExecuteSessionID:    "child-1",
		CyclePromptPath:         promptPath,
		CyclePromptSHA256:       promptHash,
		PreRunScopedFingerprint: "scope-fingerprint",
		ChildIdentity:           &identity,
	}}

	progress := planexecute.NewProgressState("child-1", prompt, promptPath, nil, nil, testNow())
	progress.Phase = planexecute.ProgressPhaseReviewRepair
	if _, err := planexecute.SaveProgressState(root, &progress); err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	assessment, err := AssessResume(ResumeAssessmentInput{
		Root:                     root,
		State:                    state,
		CurrentChildIdentity:     &identity,
		CurrentScopedFingerprint: "scope-fingerprint",
	})
	if err != nil {
		t.Fatalf("AssessResume error = %v", err)
	}
	if assessment.Blocked {
		t.Fatalf("resume unexpectedly blocked: %#v", assessment)
	}
	if !assessment.HasIncompleteCycle || !assessment.CanResumeChild {
		t.Fatalf("assessment did not detect resumable child: %#v", assessment)
	}
	if assessment.CyclePrompt != prompt {
		t.Fatalf("CyclePrompt = %q, want %q", assessment.CyclePrompt, prompt)
	}
	if assessment.ChildResumeID != "child-1" || assessment.ChildProgressPath == "" {
		t.Fatalf("child resume metadata missing: %#v", assessment)
	}
}

func TestAssessResumeBlocksCyclePromptHashMismatch(t *testing.T) {
	root := t.TempDir()
	state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
	promptPath, _, err := SaveCyclePrompt(root, state.SessionID, 1, "changed prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := testChildIdentity()
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               "dispatched",
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    sha256Hex("original prompt"),
		ChildIdentity:        &identity,
	}}

	assessment, err := AssessResume(ResumeAssessmentInput{
		Root:                 root,
		State:                state,
		CurrentChildIdentity: &identity,
	})
	if err != nil {
		t.Fatalf("AssessResume error = %v", err)
	}
	if !assessment.Blocked || assessment.BlockReason != ResumeBlockPromptMismatch {
		t.Fatalf("assessment = %#v, want prompt mismatch blocker", assessment)
	}
}

func TestAssessResumeBlocksChildProgressPromptMismatch(t *testing.T) {
	root := t.TempDir()
	prompt := "cycle prompt"
	state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, prompt)
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}

	identity := testChildIdentity()
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               "dispatched",
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    promptHash,
		ChildIdentity:        &identity,
	}}
	progress := planexecute.NewProgressState("child-1", "different prompt", promptPath, nil, nil, testNow())
	if _, err := planexecute.SaveProgressState(root, &progress); err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	assessment, err := AssessResume(ResumeAssessmentInput{
		Root:                 root,
		State:                state,
		CurrentChildIdentity: &identity,
	})
	if err != nil {
		t.Fatalf("AssessResume error = %v", err)
	}
	if !assessment.Blocked || assessment.BlockReason != ResumeBlockPromptMismatch {
		t.Fatalf("assessment = %#v, want child prompt mismatch blocker", assessment)
	}
}

func TestAssessResumeBlocksChildIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	saved := testChildIdentity()
	current := saved
	current.ToolManifestSHA256 = "different-tools"
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               "dispatched",
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    promptHash,
		ChildIdentity:        &saved,
	}}

	assessment, err := AssessResume(ResumeAssessmentInput{
		Root:                 root,
		State:                state,
		CurrentChildIdentity: &current,
	})
	if err != nil {
		t.Fatalf("AssessResume error = %v", err)
	}
	if !assessment.Blocked || assessment.BlockReason != ResumeBlockChildIdentityMismatch {
		t.Fatalf("assessment = %#v, want identity mismatch blocker", assessment)
	}
	if assessment.BlockDetail != "tool_manifest_sha256" {
		t.Fatalf("BlockDetail = %q", assessment.BlockDetail)
	}
}

func TestAssessResumeBlocksExternalScopedChange(t *testing.T) {
	root := t.TempDir()
	state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := testChildIdentity()
	state.Cycles = []CycleRecord{{
		CycleNumber:              1,
		Status:                   "dispatched",
		PlanExecuteSessionID:     "child-1",
		CyclePromptPath:          promptPath,
		CyclePromptSHA256:        promptHash,
		PostRunScopedFingerprint: "previous",
		ChildIdentity:            &identity,
	}}

	assessment, err := AssessResume(ResumeAssessmentInput{
		Root:                     root,
		State:                    state,
		CurrentChildIdentity:     &identity,
		CurrentScopedFingerprint: "current",
	})
	if err != nil {
		t.Fatalf("AssessResume error = %v", err)
	}
	if !assessment.Blocked || assessment.BlockReason != ResumeBlockExternalScopedChange {
		t.Fatalf("assessment = %#v, want scoped change blocker", assessment)
	}
}

func TestIncompleteCycleIgnoresCompletedLastCycle(t *testing.T) {
	state := IntentLoopState{Cycles: []CycleRecord{{Status: "completed"}}}
	if _, _, ok := IncompleteCycle(state); ok {
		t.Fatal("completed cycle reported incomplete")
	}
}

func TestAssessResumeReturnsNoIncompleteCycle(t *testing.T) {
	assessment, err := AssessResume(ResumeAssessmentInput{Root: t.TempDir(), State: IntentLoopState{}})
	if err != nil {
		t.Fatalf("AssessResume error = %v", err)
	}
	if assessment.HasIncompleteCycle || assessment.Blocked {
		t.Fatalf("assessment = %#v, want no incomplete cycle", assessment)
	}
}

func TestAssessResumeRejectsUnsafePromptPath(t *testing.T) {
	root := t.TempDir()
	identity := testChildIdentity()
	state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               "dispatched",
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      "../outside.md",
		CyclePromptSHA256:    sha256Hex("prompt"),
		ChildIdentity:        &identity,
	}}

	assessment, err := AssessResume(ResumeAssessmentInput{
		Root:                 root,
		State:                state,
		CurrentChildIdentity: &identity,
	})
	if err != nil {
		t.Fatalf("AssessResume error = %v", err)
	}
	if !assessment.Blocked || assessment.BlockReason != ResumeBlockPromptMismatch {
		t.Fatalf("assessment = %#v, want prompt path blocker", assessment)
	}
}

func testChildIdentity() ChildRunIdentity {
	return ChildRunIdentity{
		WorkspaceRootSHA256:        "root",
		ScopeContractSHA256:        "scope",
		VerificationCommandsSHA256: "commands",
		ToolManifestSHA256:         "tools",
		BackendProfile:             "profile",
		PlannerModel:               "planner",
		ExecutorModel:              "executor",
	}
}
