package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"
)

var _ = ginkgo.Describe("Plan-execute restart recovery", ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	ginkgo.It("resumes a blocked review from durable execution evidence without repeating writes or verification", func() {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		verificationCache := t.TempDir()
		counterPath := filepath.Join(t.TempDir(), "verification-count.txt")
		t.Setenv("GOCACHE", verificationCache)
		t.Setenv("IDIOM_E2E_VERIFICATION_COUNTER", counterPath)

		writeDevelopmentFixture(root, developmentFixtureFile{
			path: "go.mod",
			body: "module example.com/resume-development\n\ngo 1.22\n",
		})
		writeDevelopmentFixture(root, developmentFixtureFile{
			path: "feature/value.go",
			body: "package feature\n\nfunc Value() int { return 1 }\n",
		})
		writeDevelopmentFixture(root, developmentFixtureFile{
			path: "feature/value_test.go",
			body: `package feature

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestValue(t *testing.T) {
	countPath := os.Getenv("IDIOM_E2E_VERIFICATION_COUNTER")
	if countPath == "" {
		t.Fatal("IDIOM_E2E_VERIFICATION_COUNTER is empty")
	}
	count := 0
	if body, err := os.ReadFile(countPath); err == nil {
		count, err = strconv.Atoi(strings.TrimSpace(string(body)))
		if err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	count++
	if err := os.WriteFile(countPath, []byte(strconv.Itoa(count)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Value(); got != 2 {
		t.Fatalf("Value() = %d, want 2", got)
	}
}
`,
		})

		cfg := config.Default()
		cfg.Workspace.Root = root
		cfg.Workspace.CWD = root
		cfg.Workspace.EnableWrites = true
		cfg.Workspace.CommandTimeoutSeconds = 30

		const verificationCommand = "go test ./feature"
		prompt := "Change feature.Value to return two while preserving the existing regression coverage."
		obligations := plancontract.EditSetObligations{}.WithPath(
			plancontract.EditSetObligationKindImplementationEdit,
			"feature/value.go",
			"development-scenario",
		)

		plannerCalls := 0
		reviewCalls := 0
		scriptedModelPeer := func(callCfg config.Config, _ string, opts planexecute.RunOptions, _ io.Writer) (string, error) {
			switch opts.TaskFamily {
			case string(autonomy.TaskFamilyPlanThenExecutePlanner):
				plannerCalls++
				gomega.Expect(plannerCalls).To(gomega.Equal(1))
				gomega.Expect(opts.ScopeContract.EditSetObligations).To(gomega.Equal(obligations))

				plan := planartifact.PlanArtifact{
					SchemaVersion: planartifact.SchemaVersion,
					Task: planartifact.PlanArtifactTask{
						ID:      "durable-resume",
						Summary: "Change Value while retaining its existing regression coverage.",
					},
					Contract: planartifact.PlanArtifactContract{
						Kind:              planartifact.ContractKindImplementationResult,
						PermissionProfile: "guarded_workspace_write",
						RequiredEditPaths: []string{"feature/value.go", "feature/value_test.go"},
					},
					Paths: planartifact.PlanArtifactPaths{
						Required:        []string{"feature/value.go", "feature/value_test.go"},
						Editable:        []string{"feature/value.go"},
						Changed:         []string{"feature/value.go"},
						SourceSatisfied: []string{"feature/value_test.go"},
					},
					Evidence: []planartifact.PlanArtifactEvidence{{
						ID:      "existing-regression",
						Kind:    planartifact.EvidenceKindHarnessObservedSource,
						Path:    "feature/value_test.go",
						Summary: "the current regression test already asserts the required return value",
					}},
					VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
						ID:       "value-regression",
						Kind:     planartifact.VerificationKindExecutorLaneSanity,
						Command:  verificationCommand,
						Owner:    planartifact.OwnerPlanner,
						Status:   planartifact.VerificationStatusOpen,
						Required: true,
					}},
					Edits: []planartifact.PlanEdit{{
						Path:    "feature/value.go",
						OldText: "return 1",
						NewText: "return 2",
						Purpose: "satisfy the existing Value regression contract",
					}},
				}
				result := workspace.NewManager(callCfg.Workspace).Execute(context.Background(), workspace.ToolRequest{
					Tool:         workspace.ToolNameSubmitPlan,
					Rationale:    "Value needs one implementation edit; its regression test already covers the requested behavior.",
					PlanArtifact: &plan,
				})
				if strings.TrimSpace(result.Error) != "" {
					return "", fmt.Errorf("submit plan: %s", result.Error)
				}

				return "Plan submitted.", nil
			case string(autonomy.TaskFamilyPlanThenExecuteReview):
				reviewCalls++
				if reviewCalls == 1 {
					return "<<QUESTION>>: operator review was interrupted after verified execution; resume this durable session.", nil
				}

				return "Decision: pass\n\nVerification Checklist:\n- Required behavior: feature.Value returns two and the persisted regression source covers that behavior.\n- Test or command evidence: persisted executor evidence records go test ./feature as successful.\n- Scope safety: no corrective edit is needed.", nil
			default:
				return "", fmt.Errorf("unexpected task family %q", opts.TaskFamily)
			}
		}

		var firstStdout, firstStderr bytes.Buffer
		first := Run(cfg, planexecute.Options{
			Prompt:                           prompt,
			JSONEvents:                       true,
			RequiredEditPaths:                planexecute.MultiFlag{"feature/value.go", "feature/value_test.go"},
			EditSetObligations:               obligations,
			VerificationPlanExplicitCommands: []string{verificationCommand},
		}, Options{}, scriptedModelPeer, nil, &firstStdout, &firstStderr)

		gomega.Expect(first.ExitCode).To(gomega.Equal(1), "stdout=%s\nstderr=%s", firstStdout.String(), firstStderr.String())
		gomega.Expect(first.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureReviewRepairBlocked)), "result=%#v\nstdout=%s\nstderr=%s", first, firstStdout.String(), firstStderr.String())
		gomega.Expect(first.ResumeStatePath).NotTo(gomega.BeEmpty())

		blocked, _, found, err := planexecute.TryLoadProgressState(root, first.SessionID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(blocked.Phase).To(gomega.Equal(plancontract.PlanExecuteProgressPhaseReviewRepair))
		gomega.Expect(blocked.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusBlocked))
		gomega.Expect(blocked.EditSetObligations).To(gomega.Equal(obligations))
		gomega.Expect(blocked.Executor.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusCompleted))
		gomega.Expect(blocked.Executor.Succeeded).To(gomega.Equal(1))
		gomega.Expect(blocked.Executor.SourceSanity).To(gomega.ContainElement(gomega.And(
			gomega.HaveField("Path", "feature/value.go"),
			gomega.HaveField("Status", plancontract.PhaseEvidenceStatusOK),
		)))
		gomega.Expect(blocked.Executor.VerificationCommands).To(gomega.ContainElement(gomega.And(
			gomega.HaveField("Command", verificationCommand),
			gomega.HaveField("Source", plancontract.VerificationCommandSourceExplicit),
		)))
		gomega.Expect(blocked.Executor.Verification).To(gomega.ContainElement(gomega.And(
			gomega.HaveField("Command", verificationCommand),
			gomega.HaveField("Status", plancontract.PhaseEvidenceStatusOK),
		)))

		persistedPlan, err := workspace.LoadPlanArtifact(blocked.ResolvedPlanPath(root))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(persistedPlan.Paths.SourceSatisfied).To(gomega.Equal([]string{"feature/value_test.go"}))
		gomega.Expect(persistedPlan.Evidence).To(gomega.ContainElement(gomega.And(
			gomega.HaveField("Kind", planartifact.EvidenceKindHarnessObservedSource),
			gomega.HaveField("Path", "feature/value_test.go"),
		)))

		counterBody, err := os.ReadFile(counterPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(counterBody)).To(gomega.Equal("1\n"))
		valuePath := filepath.Join(root, "feature", "value.go")
		valueInfo, err := os.Stat(valuePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.RemoveAll(verificationCache)).To(gomega.Succeed())

		var resumedStdout, resumedStderr bytes.Buffer
		resumed := Run(cfg, planexecute.Options{
			Prompt:     prompt,
			Resume:     first.SessionID,
			JSONEvents: true,
		}, Options{}, scriptedModelPeer, nil, &resumedStdout, &resumedStderr)

		gomega.Expect(resumed.ExitCode).To(gomega.Equal(0), "stdout=%s\nstderr=%s", resumedStdout.String(), resumedStderr.String())
		gomega.Expect(plannerCalls).To(gomega.Equal(1), "resume must not rerun planning or execution")
		gomega.Expect(reviewCalls).To(gomega.Equal(2))
		gomega.Expect(resumed.Completion).NotTo(gomega.BeNil())
		gomega.Expect(resumed.Completion.RequiredPathCoverage).To(gomega.ContainElements(
			gomega.And(
				gomega.HaveField("Path", "feature/value.go"),
				gomega.HaveField("Status", plancontract.PlanExecuteRequiredPathCoverageChanged),
			),
			gomega.And(
				gomega.HaveField("Path", "feature/value_test.go"),
				gomega.HaveField("Status", plancontract.PlanExecuteRequiredPathCoverageSourceSatisfied),
			),
		))
		gomega.Expect(resumed.Completion.SourceSatisfiedEvidencePaths).To(gomega.Equal([]string{"feature/value_test.go"}))
		gomega.Expect(resumed.Completion.Verification).To(gomega.ContainElement(gomega.And(
			gomega.HaveField("Command", verificationCommand),
			gomega.HaveField("Status", plancontract.PhaseEvidenceStatusOK),
		)))

		completed, _, found, err := planexecute.TryLoadProgressState(root, first.SessionID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(completed.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusCompleted))
		gomega.Expect(completed.EditSetObligations).To(gomega.Equal(blocked.EditSetObligations))
		gomega.Expect(completed.ScopeContractID).To(gomega.Equal(blocked.ScopeContractID))
		gomega.Expect(completed.Executor.SourceSanity).To(gomega.Equal(blocked.Executor.SourceSanity))
		gomega.Expect(completed.Executor.VerificationCommands).To(gomega.Equal(blocked.Executor.VerificationCommands))
		gomega.Expect(completed.Executor.Verification).To(gomega.Equal(blocked.Executor.Verification))

		counterBody, err = os.ReadFile(counterPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(counterBody)).To(gomega.Equal("1\n"), "resume must not rerun persisted verification")
		resumedValueInfo, err := os.Stat(valuePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(resumedValueInfo.ModTime()).To(gomega.Equal(valueInfo.ModTime()), "resume must not reapply the completed edit")
		valueBody, err := os.ReadFile(valuePath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(valueBody)).To(gomega.ContainSubstring("return 2"))
	})
})
