package productworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type completionEffortFixture struct {
	effortRoot     string
	parityRoot     string
	repositoryRoot string
	gitPath        string
}

type completionCallResult struct {
	result CreateResult
	err    error
}

type completionCommitSnapshot struct {
	workflowBody []byte
	workflow     WorkflowState
	eventsBody   []byte
	eventsExist  bool
}

// completionGitSnapshotGate is a real-process test peer: its executable
// delegates every command to the discovered git binary and only coordinates
// the second history read so the transaction interleaving is deterministic.
type completionGitSnapshotGate struct {
	readyPath   string
	releasePath string
}

var _ = ginkgo.Describe(
	"CompleteEffort exact input transaction",
	ginkgo.Serial,
	ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		ginkgo.It("completes when the evaluated input set remains unchanged", func() {
			fixture := newCompletionEffortFixture(false)

			result, err := CompleteEffort(fixture.effortRoot)

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", result.Blockers)
			gomega.Expect(result.CurrentPhase).To(gomega.Equal(PhaseComplete))
		})

		ginkgo.DescribeTable(
			"blocks a commit when a consumed external input changes after evaluation",
			func(ctx ginkgo.SpecContext, mutate func(completionEffortFixture) error, blocker string) {
				requireCompletionGitGate()
				fixture := newCompletionEffortFixture(true)
				before := captureCompletionCommitSnapshot(fixture.effortRoot)
				gitGate := stageCompletionGitSnapshotGate(fixture.gitPath, 1)
				completed := beginCompleteEffort(fixture.effortRoot)

				gomega.Eventually(ctx, gitGate.readyPath).Should(gomega.BeAnExistingFile())
				mutationErr := mutate(fixture)
				releaseErr := os.WriteFile(gitGate.releasePath, []byte("continue\n"), 0o600)
				var outcome completionCallResult
				gomega.Eventually(ctx, completed).Should(gomega.Receive(&outcome))

				gomega.Expect(mutationErr).NotTo(gomega.HaveOccurred())
				gomega.Expect(releaseErr).NotTo(gomega.HaveOccurred())
				assertCompletionBlockedWithoutCommit(fixture.effortRoot, before, outcome, blocker)
			},
			ginkgo.Entry(
				"filtered reports membership",
				func(fixture completionEffortFixture) error {
					return os.WriteFile(filepath.Join(fixture.effortRoot, "reports", "added.json"), []byte("{}\n"), 0o644)
				},
				"completion external input effort-directory-membership:reports",
				ginkgo.SpecTimeout(30*time.Second),
			),
			ginkgo.Entry(
				"filtered briefs membership",
				func(fixture completionEffortFixture) error {
					return os.WriteFile(filepath.Join(fixture.effortRoot, "briefs", "added.md"), []byte("# Added\n"), 0o644)
				},
				"completion external input effort-directory-membership:briefs",
				ginkgo.SpecTimeout(30*time.Second),
			),
			ginkgo.Entry(
				"Obsidian file body",
				func(fixture completionEffortFixture) error {
					return os.WriteFile(
						filepath.Join(fixture.parityRoot, ObsidianProductWorkflowPath),
						[]byte("> **Status: CHANGED AFTER EVALUATION**\n"),
						0o644,
					)
				},
				"completion external input obsidian-file:",
				ginkgo.SpecTimeout(30*time.Second),
			),
		)

		ginkgo.It("blocks a commit when real Git history changes after evaluation", ginkgo.Serial, func(ctx ginkgo.SpecContext) {
			requireCompletionGitGate()
			control := newCompletionEffortFixture(true)
			controlResult, err := CompleteEffort(control.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(controlResult.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", controlResult.Blockers)
			gomega.Expect(controlResult.CurrentPhase).To(gomega.Equal(PhaseComplete))

			fixture := newCompletionEffortFixture(true)
			before := captureCompletionCommitSnapshot(fixture.effortRoot)
			gitGate := stageCompletionGitSnapshotGate(fixture.gitPath, 2)
			completed := beginCompleteEffort(fixture.effortRoot)

			gomega.Eventually(ctx, gitGate.readyPath).Should(gomega.BeAnExistingFile())
			mutationErr := mutateCompletionGitHistory(fixture)
			releaseErr := os.WriteFile(gitGate.releasePath, []byte("continue\n"), 0o600)
			var outcome completionCallResult
			gomega.Eventually(ctx, completed).Should(gomega.Receive(&outcome))

			gomega.Expect(mutationErr).NotTo(gomega.HaveOccurred())
			gomega.Expect(releaseErr).NotTo(gomega.HaveOccurred())
			assertCompletionBlockedWithoutCommit(
				fixture.effortRoot,
				before,
				outcome,
				"completion external input git-changed-paths:",
			)
		}, ginkgo.SpecTimeout(30*time.Second))

		ginkgo.DescribeTable(
			"ignores directory members outside the consumed report and brief sets",
			func(relPath, kind, initialName, irrelevantName string, irrelevantDirectory bool) {
				rootPath := ginkgo.GinkgoT().TempDir()
				gomega.Expect(os.MkdirAll(filepath.Join(rootPath, relPath), 0o755)).To(gomega.Succeed())
				gomega.Expect(os.WriteFile(filepath.Join(rootPath, relPath, initialName), []byte("{}\n"), 0o644)).To(gomega.Succeed())

				root, err := os.OpenRoot(rootPath)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.DeferCleanup(root.Close)

				entries, err := readEffortArtifactDirectoryRoot(root, relPath)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				recorder := newWorkflowCompletionInputRecorder()
				recorder.ObserveExternal(lintDirectoryCompletionInput(root, relPath, kind, entries, nil))

				irrelevantPath := filepath.Join(rootPath, relPath, irrelevantName)
				if irrelevantDirectory {
					gomega.Expect(os.MkdirAll(irrelevantPath, 0o755)).To(gomega.Succeed())
				} else {
					gomega.Expect(os.WriteFile(irrelevantPath, []byte("ignored\n"), 0o644)).To(gomega.Succeed())
				}
				gomega.Expect(recorder.Verify(root)).To(gomega.Succeed(), "irrelevant directory membership must not invalidate completion")
			},
			ginkgo.Entry("reports ignore non-JSON files", "reports", "reports-json-regular", "initial.json", "notes.txt", false),
			ginkgo.Entry("briefs ignore nested directories", "briefs", "briefs-nondirectory", "initial.md", "nested", true),
		)
	},
)

func newCompletionEffortFixture(withGit bool) completionEffortFixture {
	ginkgo.GinkgoHelper()

	fixture := completionEffortFixture{}
	if withGit {
		gitPath, err := exec.LookPath("git")
		if err != nil {
			ginkgo.Skip("git is unavailable")
		}
		fixture.gitPath = gitPath
		fixture.repositoryRoot = ginkgo.GinkgoT().TempDir()
		fixture.parityRoot = fixture.repositoryRoot
		fixture.effortRoot = filepath.Join(fixture.repositoryRoot, "effort")
		runCompletionGit(gitPath, fixture.repositoryRoot, "init")
	} else {
		fixture.effortRoot = ginkgo.GinkgoT().TempDir()
		fixture.parityRoot = fixture.effortRoot
	}

	writeCompletableCompletionEffort(fixture.effortRoot, withGit)
	writeCompletionObsidianFixture(fixture.parityRoot)
	if withGit {
		gomega.Expect(os.WriteFile(filepath.Join(fixture.effortRoot, "tracked.txt"), []byte("initial\n"), 0o644)).To(gomega.Succeed())
		runCompletionGit(fixture.gitPath, fixture.repositoryRoot, "add", "--all")
		runCompletionGit(
			fixture.gitPath,
			fixture.repositoryRoot,
			"-c", "user.name=Idiom Tests",
			"-c", "user.email=idiom-tests@example.invalid",
			"commit", "-m", "seed completion inputs",
		)
	}

	return fixture
}

func writeCompletableCompletionEffort(effortRoot string, includeGitChangeRecord bool) {
	ginkgo.GinkgoHelper()

	_, err := InitializeEffort(CreateEffortOptions{
		EffortRoot: effortRoot,
		EffortID:   "prd-adr-brief-workflow",
		Title:      "Completion transaction fixture",
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	advanceTestEffortToADRDrafting(effortRoot)

	adr := validADRDoc()
	persisted := persistInitialTestADRDrafts(effortRoot, []ADRDocument{adr}, []ADRIndexEntry{{
		ADRID:        adr.FrontMatter.ADRID,
		Title:        "Completion transaction",
		Status:       string(ArtifactStateDraft),
		Path:         "ADR-001.md",
		CoverageTier: adr.FrontMatter.CoverageTier,
		PRDRefs:      append([]string(nil), adr.FrontMatter.PRDRefs...),
		CreatedAt:    "2026-07-12T00:00:00Z",
	}})
	gomega.Expect(persisted.Documents).To(gomega.HaveLen(1))
	_, err = SubmitADRsForReview(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	approval, intakes, err := ApproveADRGate(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(approval.CurrentPhase).To(gomega.Equal(PhaseTDDSynthesis))
	for _, intake := range intakes {
		gomega.Expect(intake.Passed).To(gomega.BeTrue(), "failures=%+v", intake.Failures)
	}

	lease, err := openProductWorkflowMutationLease(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	tddGeneration, _, workflow, inspectErr := inspectTDDGeneration(lease.root)
	lease.Close()
	gomega.Expect(inspectErr).NotTo(gomega.HaveOccurred())

	tdd := TDDLedger{
		SchemaVersion: "idiom.tdd_ledger.v1",
		EffortID:      workflow.EffortID,
		TaskID:        workflow.TaskID,
		CoverageTier:  "unit",
		Coverage:      TDDCoverage{MutationMinScore: 80},
		Obligations: []TDDObligation{{
			ID:             "T1",
			Type:           "acceptance",
			Behavior:       "Completion commits only fresh evaluated inputs",
			Owner:          "internal/workflows/productworkflow/completion_transaction_e2e_test.go",
			Command:        "go test ./internal/workflows/productworkflow",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	}
	tddBody := marshalCompletionJSON(tdd)
	gomega.Expect(commitSynthesizedTDDLedger(effortRoot, tddGeneration, tddBody)).To(gomega.Succeed())

	gomega.Expect(os.MkdirAll(filepath.Join(effortRoot, "evidence"), 0o755)).To(gomega.Succeed())
	evidence := EvidenceCard{
		CardID:       "E1",
		Kind:         "test",
		SourceHandle: "completion-transaction",
		Claim:        "Completion must reject stale evaluated inputs",
		Confidence:   "high",
		Freshness:    "current",
		Uncertainty:  "none",
		EvidenceRefs: []string{"completion-transaction-e2e"},
		ResearcherID: "test",
		Timestamp:    "2026-07-12T00:00:00Z",
	}
	gomega.Expect(os.WriteFile(
		filepath.Join(effortRoot, "evidence", "evidence.jsonl"),
		marshalCompletionJSON(evidence),
		0o644,
	)).To(gomega.Succeed())
	writeCompletionJSON(filepath.Join(effortRoot, "evidence", "traceability_matrix.json"), TraceabilityMatrix{
		SchemaVersion: "idiom.traceability.v1",
		EffortID:      workflow.EffortID,
		TaskID:        workflow.TaskID,
		CreatedAt:     "2026-07-12T00:00:00Z",
		Matrix: []TraceabilityRow{{
			EvidenceCard:   "E1",
			PRDRequirement: "REQ-001",
			ADR:            "ADR-001",
			TDDObligation:  "T1",
			Brief:          "briefs/initial.md",
			Report:         "reports/impl.json",
		}},
	})

	gomega.Expect(os.MkdirAll(filepath.Join(effortRoot, "briefs"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(effortRoot, "briefs", "initial.md"), []byte("# Initial brief\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(filepath.Join(effortRoot, "reports"), 0o755)).To(gomega.Succeed())
	for _, name := range []string{"impl.json", "invest.json", "review.json"} {
		gomega.Expect(os.WriteFile(filepath.Join(effortRoot, "reports", name), []byte("{}\n"), 0o644)).To(gomega.Succeed())
	}

	records := []ImplementationLedgerRecord{
		{
			RecordType:           LedgerRecordAtomPlanned,
			TaskID:               workflow.TaskID,
			EffortID:             workflow.EffortID,
			Timestamp:            "2026-05-17T15:00:00Z",
			ComplexityTier:       "C1",
			ContextPackID:        "completion-transaction",
			Scope:                &AtomScope{ExpectedFiles: []string{"tracked.txt"}, Forbidden: []string{"secrets/"}},
			RequiredVerification: []string{"go test ./internal/workflows/productworkflow"},
			ReportPaths:          &ReportPaths{Implementation: "reports/impl.json", Review: "reports/review.json", Investigation: "reports/invest.json"},
			Implementor:          &ImplementorAssignment{ID: "impl", Runtime: "local", Model: "test", Command: "test"},
			Reviewer:             &ReviewerAssignment{ID: "review", Runtime: "local", Model: "test", ReasoningEffort: "high"},
		},
	}
	if includeGitChangeRecord {
		records = append(records, ImplementationLedgerRecord{
			RecordType:   LedgerRecordImplementationCompleted,
			TaskID:       workflow.TaskID,
			EffortID:     workflow.EffortID,
			Timestamp:    "2026-05-17T16:00:00Z",
			ChangedPaths: []string{"tracked.txt"},
			CodeChanges:  true,
		})
	}
	records = append(records, ImplementationLedgerRecord{
		RecordType:              LedgerRecordVerificationCompleted,
		TaskID:                  workflow.TaskID,
		EffortID:                workflow.EffortID,
		Timestamp:               "2026-05-17T17:00:00Z",
		TDDObligationsSatisfied: []string{"T1"},
		VerificationStatus:      "pass",
		VerificationCommandsRun: []string{"go test ./internal/workflows/productworkflow"},
		Actor:                   "verifier",
	})
	writeCompletionJSONLines(filepath.Join(effortRoot, "implementation_ledger.jsonl"), records)
}

func writeCompletionObsidianFixture(root string) {
	ginkgo.GinkgoHelper()

	productPath := filepath.Join(root, ObsidianProductWorkflowPath)
	recoveryPath := filepath.Join(root, ObsidianRecoveryPath)
	gomega.Expect(os.MkdirAll(filepath.Dir(productPath), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(productPath, []byte("> **Status: CURRENT**\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(recoveryPath, []byte("# Recovery\n"), 0o644)).To(gomega.Succeed())
}

func captureCompletionCommitSnapshot(effortRoot string) completionCommitSnapshot {
	ginkgo.GinkgoHelper()

	workflowBody, err := os.ReadFile(filepath.Join(effortRoot, WorkflowStateFileName))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	workflow, err := ParseWorkflowStateJSON(workflowBody)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	eventsPath, err := productWorkflowEventsPath(workflow)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	eventsBody, eventsExist, err := readOptionalCompletionFile(filepath.Join(effortRoot, filepath.FromSlash(eventsPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return completionCommitSnapshot{
		workflowBody: workflowBody,
		workflow:     workflow,
		eventsBody:   eventsBody,
		eventsExist:  eventsExist,
	}
}

func assertCompletionBlockedWithoutCommit(
	effortRoot string,
	before completionCommitSnapshot,
	outcome completionCallResult,
	blocker string,
) {
	ginkgo.GinkgoHelper()

	gomega.Expect(outcome.err).NotTo(gomega.HaveOccurred())
	gomega.Expect(outcome.result.Status).To(gomega.Equal(ProductWorkflowStatusBlocked))
	gomega.Expect(outcome.result.CurrentPhase).To(gomega.Equal(PhaseTDDSynthesis))
	gomega.Expect(strings.Join(outcome.result.Blockers, "\n")).To(gomega.ContainSubstring(blocker))

	afterBody, err := os.ReadFile(filepath.Join(effortRoot, WorkflowStateFileName))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(afterBody).To(gomega.Equal(before.workflowBody))
	after, err := ParseWorkflowStateJSON(afterBody)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(after.CurrentPhase).To(gomega.Equal(before.workflow.CurrentPhase))
	gomega.Expect(after.StateRevision).To(gomega.Equal(before.workflow.StateRevision))
	gomega.Expect(after.LastEventID).To(gomega.Equal(before.workflow.LastEventID))

	eventsPath, err := productWorkflowEventsPath(before.workflow)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	afterEvents, afterEventsExist, err := readOptionalCompletionFile(filepath.Join(effortRoot, filepath.FromSlash(eventsPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(afterEventsExist).To(gomega.Equal(before.eventsExist))
	gomega.Expect(afterEvents).To(gomega.Equal(before.eventsBody))
}

func beginCompleteEffort(effortRoot string) <-chan completionCallResult {
	completed := make(chan completionCallResult, 1)
	go func() {
		result, err := CompleteEffort(effortRoot)
		completed <- completionCallResult{result: result, err: err}
	}()

	return completed
}

func requireCompletionGitGate() {
	ginkgo.GinkgoHelper()

	if _, err := os.Stat("/bin/sh"); err != nil {
		ginkgo.Skip("deterministic completion transaction interleaving requires /bin/sh")
	}
}

func stageCompletionGitSnapshotGate(realGitPath string, blockedLogCall int) completionGitSnapshotGate {
	ginkgo.GinkgoHelper()
	gomega.Expect(blockedLogCall).To(gomega.BeElementOf(1, 2))

	directory := ginkgo.GinkgoT().TempDir()
	readyPath := filepath.Join(directory, "ready")
	releasePath := filepath.Join(directory, "release")
	ginkgo.DeferCleanup(func() {
		gomega.Expect(os.WriteFile(releasePath, []byte("cleanup\n"), 0o600)).To(gomega.Succeed())
	})

	wrapperPath := filepath.Join(directory, "git")
	wrapper := `#!/bin/sh
is_log=
for argument in "$@"; do
	if [ "$argument" = "log" ]; then
		is_log=1
	fi
done
if [ -n "$is_log" ]; then
	log_call=2
	if [ ! -e "$IDIOM_COMPLETION_GIT_FIRST_LOG" ]; then
		: > "$IDIOM_COMPLETION_GIT_FIRST_LOG"
		log_call=1
	fi
	if [ "$log_call" = "$IDIOM_COMPLETION_GIT_BLOCK_LOG" ] && [ ! -e "$IDIOM_COMPLETION_GIT_BLOCKED" ]; then
		: > "$IDIOM_COMPLETION_GIT_BLOCKED"
		: > "$IDIOM_COMPLETION_GIT_READY"
		while [ ! -e "$IDIOM_COMPLETION_GIT_RELEASE" ]; do
			sleep 0.05
		done
	fi
fi
exec "$IDIOM_COMPLETION_REAL_GIT" "$@"
`
	gomega.Expect(os.WriteFile(wrapperPath, []byte(wrapper), 0o755)).To(gomega.Succeed())
	ginkgo.GinkgoT().Setenv("IDIOM_COMPLETION_REAL_GIT", realGitPath)
	ginkgo.GinkgoT().Setenv("IDIOM_COMPLETION_GIT_FIRST_LOG", filepath.Join(directory, "first-log-seen"))
	ginkgo.GinkgoT().Setenv("IDIOM_COMPLETION_GIT_BLOCKED", filepath.Join(directory, "blocked-log-seen"))
	ginkgo.GinkgoT().Setenv("IDIOM_COMPLETION_GIT_BLOCK_LOG", fmt.Sprintf("%d", blockedLogCall))
	ginkgo.GinkgoT().Setenv("IDIOM_COMPLETION_GIT_READY", readyPath)
	ginkgo.GinkgoT().Setenv("IDIOM_COMPLETION_GIT_RELEASE", releasePath)
	ginkgo.GinkgoT().Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))

	return completionGitSnapshotGate{readyPath: readyPath, releasePath: releasePath}
}

func mutateCompletionGitHistory(fixture completionEffortFixture) error {
	trackedPath := filepath.Join(fixture.effortRoot, "tracked.txt")
	if err := os.WriteFile(trackedPath, []byte("changed after evaluation\n"), 0o644); err != nil {
		return err
	}
	if err := runCompletionGitError(fixture.gitPath, fixture.repositoryRoot, "add", "effort/tracked.txt"); err != nil {
		return err
	}

	return runCompletionGitError(
		fixture.gitPath,
		fixture.repositoryRoot,
		"-c", "user.name=Idiom Tests",
		"-c", "user.email=idiom-tests@example.invalid",
		"commit", "-m", "mutate consumed completion history",
	)
}

func writeCompletionJSON(path string, value any) {
	ginkgo.GinkgoHelper()

	gomega.Expect(os.WriteFile(path, marshalCompletionJSON(value), 0o644)).To(gomega.Succeed())
}

func writeCompletionJSONLines(path string, values []ImplementationLedgerRecord) {
	ginkgo.GinkgoHelper()

	var body []byte
	for _, value := range values {
		body = append(body, marshalCompletionJSON(value)...)
	}
	gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())
}

func marshalCompletionJSON(value any) []byte {
	ginkgo.GinkgoHelper()

	body, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return append(body, '\n')
}

func readOptionalCompletionFile(path string) ([]byte, bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	return body, true, nil
}

func runCompletionGit(gitPath, root string, args ...string) {
	ginkgo.GinkgoHelper()

	gomega.Expect(runCompletionGitError(gitPath, root, args...)).To(gomega.Succeed())
}

func runCompletionGitError(gitPath, root string, args ...string) error {
	commandArgs := append([]string{"-C", root}, args...)
	output, err := exec.Command(gitPath, commandArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %s: %w", strings.Join(args, " "), string(output), err)
	}

	return nil
}
