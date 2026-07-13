package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/recovery"
)

const mutationResumeTestLabel = "mutation-state-resume"

func mutationResumeImplementationState() *recoverycontract.ImplementationState {
	scope := &recoverycontract.CanonicalMutationScopeProjection{
		RequiredCoverageIDs: []string{"target.go"},
		Rules: []recoverycontract.CanonicalMutationScopeRuleProjection{{
			Kind: "exact_file", Source: "required_path", CanonicalID: "target.go",
		}},
		Presentations: []recoverycontract.CanonicalMutationScopePresentationProjection{{
			PresentedPath: "target.go", CanonicalID: "target.go", Source: "required_path",
		}},
	}

	return &recoverycontract.ImplementationState{
		Phase:                  "implement",
		ChangedPaths:           []string{"target.go"},
		BlockedTaskLedgerItems: []recoverycontract.ImplementationBlockedTaskItem{},
		CanonicalMutationScope: cloneRecoveryCanonicalMutationScope(scope),
		CommitReceipts: []recoverycontract.MutationCommitProjection{{
			Scope: cloneRecoveryCanonicalMutationScope(scope),
			Effects: []recoverycontract.MutationCommitEffectProjection{{
				CanonicalID: "target.go", Kind: "replaced_file", Role: "file",
			}},
			PostimageFileIDs: []string{"target.go"},
			AffectedFileIDs:  []string{"target.go"},
		}},
	}
}

func writeMutationResumeFailure(t ginkgo.FullGinkgoTInterface, root, sessionID string, implementation *recoverycontract.ImplementationState) string {
	ginkgo.GinkgoHelper()

	_, recordPath, err := recovery.WriteFailureRecord(root, recovery.FailureRecordInput{
		FailureID: recovery.ImplementFailureID(sessionID),
		SourceRun: recoverycontract.SourceRun{
			Lane:              string(recoverycontract.SourceLaneImplement),
			SessionID:         sessionID,
			RequiredEditPaths: []string{"target.go"},
		},
		FailureClass:   recoverycontract.FailureClassExecutorEdit,
		FailureKind:    "implementation_failed",
		FailurePhase:   "implement",
		FailureSummary: "implementation stopped after a partial mutation",
		Implementation: implementation,
	}, time.Unix(1, 0).UTC())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("WriteFailureRecord: %v", err))
	return filepath.Join(root, filepath.FromSlash(recordPath))
}

func mutationResumeModel(root string, auditWriter *bytes.Buffer) *Model {
	model := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: root},
			Workspace: config.Workspace{Root: root, CWD: root},
		},
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	if auditWriter != nil {
		model.eventStream = newJSONEventStream(auditWriter, "new-session")
	}
	return model
}

var _ = ginkgo.It("keeps ordinary history resume behavior when no failure record exists", ginkgo.Label(mutationResumeTestLabel, "integration"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	logPath := writeSessionLog(t, filepath.Join(root, "logs"), "ordinary.jsonl", []string{
		`{"type":"message","role":"user","content":"continue the explanation"}`,
		`{"type":"message","role":"assistant","content":"prior answer"}`,
	})
	var audit bytes.Buffer
	model := mutationResumeModel(root, &audit)

	err := model.LoadHistoryFromSession(logPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	gomega.Expect(model.resumeImplementationState).To(gomega.BeNil())
	gomega.Expect(model.messages).To(gomega.HaveLen(2))
	gomega.Expect(audit.String()).To(gomega.ContainSubstring(`"type":"mutation_state_resume_validation"`))
	gomega.Expect(audit.String()).To(gomega.ContainSubstring(`"status":"missing"`))
})

var _ = ginkgo.It("loads and clones durable mutation state for headless resume", ginkgo.Label(mutationResumeTestLabel, "development-scenario", "e2e", "integration", "smoke"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	sessionID := "headless-resume"
	logPath := writeSessionLog(t, filepath.Join(root, "logs"), sessionID+".jsonl", []string{
		`{"type":"message","role":"user","content":"implement target.go"}`,
		`{"type":"message","role":"assistant","content":"the prior run stopped"}`,
	})
	want := mutationResumeImplementationState()
	writeMutationResumeFailure(t, root, sessionID, want)
	var audit bytes.Buffer
	model := mutationResumeModel(root, nil)
	model.nonInteractive = true

	err := model.LoadHistoryFromSession(logPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	gomega.Expect(reflect.DeepEqual(model.resumeImplementationState, want)).To(gomega.BeTrue(), fmt.Sprintf("restored state = %#v, want %#v", model.resumeImplementationState, want))
	gomega.Expect(model.pendingMutationStateResumeAudit).NotTo(gomega.BeNil())
	model.SetJSONEventStream(&audit, "new-session")
	gomega.Expect(audit.String()).To(gomega.ContainSubstring(`"status":"loaded"`))
	gomega.Expect(audit.String()).To(gomega.ContainSubstring(`"commit_receipt_count":1`))
	gomega.Expect(model.pendingMutationStateResumeAudit).To(gomega.BeNil())

	detached := newDetachedPromptExecutionSession(context.Background(), model)
	detached.resumeImplementationState.CommitReceipts[0].Effects[0].CanonicalID = "mutated.go"
	gomega.Expect(model.resumeImplementationState.CommitReceipts[0].Effects[0].CanonicalID).To(gomega.Equal("target.go"))
})

var _ = ginkgo.It("restores durable mutation state through interactive ResumeSession", ginkgo.Label(mutationResumeTestLabel, "development-scenario", "e2e", "integration"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")
	sessionID := "interactive-resume"
	writeSessionLog(t, logDir, sessionID+".jsonl", []string{
		`{"type":"message","role":"user","content":"implement target.go"}`,
		`{"type":"message","role":"assistant","content":"the prior run stopped"}`,
	})
	writeMutationResumeFailure(t, root, sessionID, mutationResumeImplementationState())
	model := mutationResumeModel(root, nil)
	model.cfg.Chat.DebugLogDir = logDir
	model.cfg.Chat.DebugLogMode = "redacted"

	err := model.ResumeSession(sessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResumeSession: %v", err))
	gomega.Expect(model.resumeImplementationState).NotTo(gomega.BeNil())
	gomega.Expect(model.resumeImplementationState.CommitReceipts).To(gomega.HaveLen(1))
	if model.debugLogger != nil {
		gomega.Expect(model.debugLogger.Close()).To(gomega.Succeed())
		model.debugLogger = nil
	}

	body, readErr := os.ReadFile(filepath.Join(logDir, sessionID+".jsonl"))
	gomega.Expect(readErr).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read resumed log: %v", readErr))
	gomega.Expect(string(body)).To(gomega.ContainSubstring(mutationStateResumeEventRole))
})

var _ = ginkgo.It("fails closed when a recovery record session is tampered", ginkgo.Label(mutationResumeTestLabel, "development-scenario", "integration"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	sessionID := "tampered-resume"
	logPath := writeSessionLog(t, filepath.Join(root, "logs"), sessionID+".jsonl", []string{
		`{"type":"message","role":"user","content":"implement target.go"}`,
	})
	recordPath := writeMutationResumeFailure(t, root, sessionID, mutationResumeImplementationState())
	body, err := os.ReadFile(recordPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	body = bytes.Replace(body, []byte(`"session_id": "tampered-resume"`), []byte(`"session_id": "different-session"`), 1)
	gomega.Expect(os.WriteFile(recordPath, body, 0o600)).To(gomega.Succeed())
	var audit bytes.Buffer
	model := mutationResumeModel(root, &audit)

	err = model.LoadHistoryFromSession(logPath)
	gomega.Expect(err).To(gomega.HaveOccurred())
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("does not match resumed session"))
	gomega.Expect(model.messages).To(gomega.BeEmpty())
	gomega.Expect(model.resumeImplementationState).To(gomega.BeNil())
	gomega.Expect(audit.String()).To(gomega.ContainSubstring(`"status":"rejected"`))
	gomega.Expect(audit.String()).To(gomega.ContainSubstring(`"reason":"session_mismatch"`))
})

var _ = ginkgo.It("passes structurally valid durable projections to the runtime authenticity boundary", ginkgo.Label(mutationResumeTestLabel, "integration"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	sessionID := "receipt-mismatch"
	logPath := writeSessionLog(t, filepath.Join(root, "logs"), sessionID+".jsonl", []string{
		`{"type":"message","role":"user","content":"implement target.go"}`,
	})
	state := mutationResumeImplementationState()
	state.CommitReceipts[0].Scope.RequiredCoverageIDs = []string{"other.go"}
	writeMutationResumeFailure(t, root, sessionID, state)
	var audit bytes.Buffer
	model := mutationResumeModel(root, &audit)

	err := model.LoadHistoryFromSession(logPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	gomega.Expect(model.resumeImplementationState).NotTo(gomega.BeNil())
	gomega.Expect(model.resumeImplementationState.CommitReceipts[0].Scope.RequiredCoverageIDs).To(gomega.Equal([]string{"other.go"}))
	gomega.Expect(audit.String()).To(gomega.ContainSubstring(`"status":"loaded"`))
})

var _ = ginkgo.It("renders typed mutation resume audit events", ginkgo.Label(mutationResumeTestLabel), func() {
	event, ok := historyAuditMutationStateResume(`{"status":"mismatch","reason":"fresh_scope_disagrees_with_persisted_projection","scope_present":true,"commit_receipt_count":2}`)
	gomega.Expect(ok).To(gomega.BeTrue())
	gomega.Expect(event.kind).To(gomega.Equal("recovery"))
	gomega.Expect(event.status).To(gomega.Equal("mismatch"))
	gomega.Expect(event.label).To(gomega.Equal("mutation state resume mismatch"))
	gomega.Expect(strings.Contains(event.detail, "commit_receipts=2")).To(gomega.BeTrue(), event.detail)
})
