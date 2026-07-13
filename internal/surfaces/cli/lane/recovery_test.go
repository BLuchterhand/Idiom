package lane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	recoverycontract "idiom/internal/contracts/recovery"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/workflows/planexecute"
	planexecuterunner "idiom/internal/workflows/planexecute/runner"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.Describe("recovery", func() {
	ginkgo.It("run lane implement recovery writes failure and packet", func() {
		root := ginkgo.GinkgoT().TempDir()
		briefPath := filepath.Join(root, "brief.md")
		briefBody := "# Implement atom\n\n" +
			"raw_prompt: SECRET_PROMPT_BODY\n\n" +
			"## Files To Modify\n\n" +
			"| File | Action |\n" +
			"| --- | --- |\n" +
			"| `internal/workflows/recovery/record.go` | Update recovery records. |\n\n" +
			"## Required Edits\n\n" +
			"Update only `internal/workflows/recovery/record.go`.\n"
		gomega.Expect(os.WriteFile(briefPath, []byte(briefBody), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".planning"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, ".planning", "impl-session.failures.jsonl"), []byte(`{"kind":"bounded"}`+"\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := runLaneRecoveryTest(root, []string{"implement", "--json", "--project-root", root, briefPath},
			func(args []string, stdout, _ io.Writer, _ cmdruntime.PromptRunner) int {
				_, _ = stdout.Write([]byte(`{"type":"session_summary","session_id":"impl-session","stop_reason":"model_error"}` + "\n"))
				_, _ = stdout.Write([]byte(`{"type":"implementation_lifecycle_state","phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["internal/workflows/recovery/record.go"],"sourced_evidence_paths":["internal/workflows/recovery/record.go"],"write_ready_nudge_count":1}` + "\n"))
				_, _ = stdout.Write([]byte("transcript: SECRET_TRANSCRIPT_BODY\n"))

				return 7
			},
			func(config.Config, planexecute.Options, planexecuterunner.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult {
				return planexecute.RunResult{ExitCode: 0}
			},
			&stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(7), fmt.Sprintf("expected wrapped implement exit 7, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		record := readRecoveryRecord(filepath.Join(root, ".idiom", "recovery", "implement-impl-session", "failure.json"))
		gomega.Expect(record.SourceRun.Lane).To(gomega.Equal(string(recoverycontract.SourceLaneImplement)), fmt.Sprintf("unexpected source run: %#v", record.SourceRun))
		gomega.Expect(record.SourceRun.SessionID).To(gomega.Equal("impl-session"), fmt.Sprintf("unexpected source run: %#v", record.SourceRun))
		gomega.Expect(record.Failure.Kind).To(gomega.Equal("implement_process_failed"), fmt.Sprintf("unexpected failure: %#v", record.Failure))
		gomega.Expect(record.Failure.Class).To(gomega.Equal(recoverycontract.FailureClassExecutorEdit), fmt.Sprintf("unexpected failure: %#v", record.Failure))
		gomega.Expect(record.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition), fmt.Sprintf("next action=%q", record.NextAction))
		gomega.Expect(record.SourceRun.PromptFile).To(gomega.Equal("brief.md"), fmt.Sprintf("record should store original workspace-relative brief path, got %q", record.SourceRun.PromptFile))
		gomega.Expect(record.SourceRun.RequiredEditPaths).To(gomega.HaveLen(1), fmt.Sprintf("required edit paths should come from original brief, got %#v", record.SourceRun.RequiredEditPaths))
		gomega.Expect(record.SourceRun.RequiredEditPaths[0]).To(gomega.Equal("internal/workflows/recovery/record.go"), fmt.Sprintf("required edit paths should come from original brief, got %#v", record.SourceRun.RequiredEditPaths))
		gomega.Expect(record.Implementation).NotTo(gomega.Equal(nil), "expected implementation state in recovery record")
		gomega.Expect(record.Implementation.Phase).To(gomega.Equal("implementation.target_write_ready"), fmt.Sprintf("unexpected implementation state: %#v", record.Implementation))
		gomega.Expect(record.Implementation.StopReason).To(gomega.Equal("model_call_budget_exhausted"), fmt.Sprintf("unexpected implementation state: %#v", record.Implementation))
		gomega.Expect(record.Implementation.WriteReadyNudgeCount).To(gomega.Equal(1), fmt.Sprintf("unexpected implementation state: %#v", record.Implementation))
		gomega.Expect(record.Implementation.RemainingRequiredEditPaths).To(gomega.HaveLen(1), fmt.Sprintf("expected remaining path in implementation state, got %#v", record.Implementation))
		gomega.Expect(record.Implementation.RemainingRequiredEditPaths[0]).To(gomega.Equal("internal/workflows/recovery/record.go"), fmt.Sprintf("expected remaining path in implementation state, got %#v", record.Implementation))

		assertEvidenceRef(record.Failure.EvidenceRefs, "lane_history", ".idiom/lane-history.jsonl", "")
		assertEvidenceRef(record.Failure.EvidenceRefs, "original_brief", "brief.md", "")
		assertEvidenceRef(record.Failure.EvidenceRefs, "planning_failures", ".planning/impl-session.failures.jsonl", "")

		packet := readRecoveryPacket(filepath.Join(root, ".idiom", "recovery", "implement-impl-session", "packet.json"))
		gomega.Expect(packet.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("unexpected packet: %#v", packet))
		gomega.Expect(packet.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition), fmt.Sprintf("unexpected packet: %#v", packet))
		gomega.Expect(packet.CandidateAtoms).To(gomega.HaveLen(1), fmt.Sprintf("expected one recovery atom for original required path, got %#v", packet.CandidateAtoms))

		if paths := packet.CandidateAtoms[0].RequiredPaths; len(paths) != 1 || paths[0] != "internal/workflows/recovery/record.go" {
			ginkgo.Fail(fmt.Sprintf("expected recovery atom to target original required path, got %#v", paths))
		}
		gomega.Expect(packet.Implementation).NotTo(gomega.BeNil(), fmt.Sprintf("expected implementation state in recovery packet, got %#v", packet.Implementation))
		gomega.Expect(packet.Implementation.StopReason).To(gomega.Equal("model_call_budget_exhausted"), fmt.Sprintf("expected implementation state in recovery packet, got %#v", packet.Implementation))

		rendered, _ := json.Marshal(struct {
			Record recoverycontract.FailureRecord
			Packet recoverycontract.DecompositionPacket
		}{Record: record, Packet: packet})
		for _, forbidden := range []string{"SECRET_PROMPT_BODY", "SECRET_TRANSCRIPT_BODY", "raw_prompt", "transcript", "idiom-lane-"} {
			gomega.Expect(string(rendered)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("recovery artifacts leaked %q in %s", forbidden, string(rendered)))

		}
	})
	ginkgo.It("run lane implement recovery captures prompt runner session id", func() {
		root := ginkgo.GinkgoT().TempDir()
		briefPath := filepath.Join(root, "brief.md")
		gomega.Expect(os.WriteFile(briefPath, []byte("# Implement atom\n\nUpdate `x.go`.\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := runLaneRecoveryTest(root, []string{"implement", "--project-root", root, briefPath},
			func(args []string, stdout, _ io.Writer, promptRun cmdruntime.PromptRunner) int {
				cfg := config.Default()

				cfg.Workspace.SessionID = "captured-session"
				_, _ = promptRun(cfg, "", cmdruntime.RunOptions{}, stdout)

				return 3
			},
			func(config.Config, planexecute.Options, planexecuterunner.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult {
				return planexecute.RunResult{ExitCode: 0}
			},
			&stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(3), fmt.Sprintf("expected wrapped implement exit 3, got %d stderr=%s", code, stderr.String()))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "recovery", "implement-captured-session", "failure.json")); err != nil {
			ginkgo.Fail(fmt.Sprintf("expected recovery record to use captured prompt runner session id: %v", err))
		}
	})
	ginkgo.DescribeTable("classifies implement failures from bounded signals",
		func(summary string, want recoverycontract.FailureClass) {
			gomega.Expect(recovery.ClassifyImplementFailure(summary)).To(gomega.Equal(want))
		},
		ginkgo.Entry("executor edit failure", "implement process failed: exit_code=1 stop_reason=model_error", recoverycontract.FailureClassExecutorEdit),
		ginkgo.Entry("budget exhaustion", "implement process failed: context length exceeded", recoverycontract.FailureClassBudgetExhausted),
		ginkgo.Entry("verification failure", "implement process failed: required tests failed", recoverycontract.FailureClassVerification),
	)
	ginkgo.It("run lane plan execute recovery does not write lane implement record", func() {
		root := ginkgo.GinkgoT().TempDir()
		briefPath := filepath.Join(root, "brief.md")
		gomega.Expect(os.WriteFile(briefPath, []byte(minimalValidPlanExecuteBrief()), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := runLaneRecoveryTest(root, []string{"plan-execute", "--json", "--project-root", root, briefPath},
			func(args []string, stdout, _ io.Writer, _ cmdruntime.PromptRunner) int { return 0 },
			func(_ config.Config, _ planexecute.Options, _ planexecuterunner.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, stdout io.Writer, _ io.Writer) planexecute.RunResult {
				_, _ = stdout.Write([]byte(`{"type":"session_summary","session_id":"plan-session","stop_reason":"planner_failed"}` + "\n"))
				return planexecute.RunResult{ExitCode: 5}
			},
			&stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(5), fmt.Sprintf("expected wrapped plan-execute exit 5, got %d stderr=%s", code, stderr.String()))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "recovery", "implement-plan-session", "failure.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("plan-execute lane should not write implement recovery record, stat err=%v", err))
		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "recovery", "plan-execute-plan-session", "failure.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("lane wrapper should not double-write plan-execute recovery record, stat err=%v", err))
		}
	})
})

func runLaneRecoveryTest(root string, args []string, implement laneImplementRunner, plan lanePlanExecuteRunner, stdout, stderr io.Writer) int {

	stubLaneBriefLint(passingLaneBriefLint)

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.EnableWrites = true

	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   plan,
		runImplement:     implement,
		defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
	}

	return runLaneMainWithDeps(args, stdout, stderr, deps)
}

func readRecoveryRecord(path string) recoverycontract.FailureRecord {

	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read recovery record: %v", err))

	var record recoverycontract.FailureRecord
	gomega.Expect(json.Unmarshal(body, &record)).To(gomega.Succeed())

	return record
}

func readRecoveryPacket(path string) recoverycontract.DecompositionPacket {

	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read recovery packet: %v", err))

	var packet recoverycontract.DecompositionPacket
	gomega.Expect(json.Unmarshal(body, &packet)).To(gomega.Succeed())

	return packet
}

func assertEvidenceRef(refs []recoverycontract.EvidenceRef, kind string, path string, id string) {

	for _, ref := range refs {
		if ref.Kind == kind && ref.Path == path && ref.ID == id {
			return
		}
	}
	ginkgo.Fail(fmt.Sprintf("missing evidence ref kind=%q path=%q id=%q in %#v", kind, path, id, refs))
}
