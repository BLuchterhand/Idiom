package planexecute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	plancontract "idiom/internal/contracts/plan"
)

var _ = ginkgo.Describe("emit executor preflight failure", func() {
	mismatch := &PlanEditPreflightMismatch{
		Index:  2,
		Path:   "pkg/target.go",
		Reason: "outside allowed scope",
	}

	ginkgo.It("emits default event fields", func() {
		var stdout bytes.Buffer

		emitExecutorPreflightFailure(&stdout, true, mismatch, "allowed_edit_paths pkg/target.go", nil)

		event := decodeSinglePlanExecuteEventForTest(stdout.String())
		gomega.Expect(event["type"] != plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPreflightFailed)).To(gomega.BeFalse(), fmt.Sprintf("type = %v", event["type"]))
		gomega.Expect(event["edit_index"]).To(gomega.Equal(float64(2)), fmt.Sprintf("event fields = %+v", event))
		gomega.Expect(event["path"]).To(gomega.Equal("pkg/target.go"), fmt.Sprintf("event fields = %+v", event))
		gomega.Expect(event["reason"]).To(gomega.Equal("outside allowed scope"), fmt.Sprintf("event fields = %+v", event))

	})

	ginkgo.It("emits custom event fields", func() {
		var stdout bytes.Buffer

		emitExecutorPreflightFailure(&stdout, true, mismatch, "approval/scope pkg/target.go", map[string]any{
			"edit_index":        mismatch.Index,
			"path":              "pkg/target.go",
			"reason":            mismatch.Reason,
			"handoff_id":        "handoff-1",
			"approval_scope_id": "approval-1",
		})

		event := decodeSinglePlanExecuteEventForTest(stdout.String())
		gomega.Expect(event["handoff_id"]).To(gomega.Equal("handoff-1"), fmt.Sprintf("custom event fields = %+v", event))
		gomega.Expect(event["approval_scope_id"]).To(gomega.Equal("approval-1"), fmt.Sprintf("custom event fields = %+v", event))

	})

	ginkgo.It("emits human-readable output", func() {
		var stdout bytes.Buffer

		emitExecutorPreflightFailure(&stdout, false, mismatch, "edit 3/4 pkg/target.go", nil)

		if got, want := stdout.String(), "  preflight FAILED on edit 3/4 pkg/target.go: outside allowed scope\n"; got != want {
			ginkgo.Fail(fmt.Sprintf("stdout = %q, want %q", got, want))
		}
	})
})

var _ = ginkgo.It("emit plan execute json event returns marshal error", func() {
	var stdout bytes.Buffer
	err := emitPlanExecuteJSONEvent(&stdout, true, plancontract.PlanExecuteEventVerification, map[string]any{
		"bad": func() {},
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected marshal error"))
	gomega.Expect(stdout.Len() != 0).To(gomega.BeFalse(), fmt.Sprintf("stdout = %q, want empty on marshal failure", stdout.String()))
})

var _ = ginkgo.It("emit plan execute json event rejects unknown event type", func() {
	var stdout bytes.Buffer
	err := emitPlanExecuteJSONEvent(&stdout, true, plancontract.PlanExecuteEventType("typo_event"), nil)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unknown event type error, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unknown plan-execute event type"), fmt.Sprintf("expected unknown event type error, got %v", err))
	gomega.Expect(stdout.Len() != 0).To(gomega.BeFalse(), fmt.Sprintf("stdout = %q, want empty on unknown event type", stdout.String()))
})

var _ = ginkgo.It("emit plan execute invalid json event rejects unknown event type", func() {
	var stdout bytes.Buffer
	err := emitPlanExecuteInvalidJSONEvent(&stdout, true, plancontract.PlanExecuteEventType("typo_event"), nil)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unknown invalid event type error, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unknown plan-execute invalid event type"), fmt.Sprintf("expected unknown invalid event type error, got %v", err))
	gomega.Expect(stdout.Len() != 0).To(gomega.BeFalse(), fmt.Sprintf("stdout = %q, want empty on unknown invalid event type", stdout.String()))
})

var _ = ginkgo.It("run plan execute verification command times out", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module timeoutverify\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write go.mod: %v", err))
	}
	if err := os.Mkdir(filepath.Join(root, "slow"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir slow: %v", err))
	}
	testSource := `package slow

import (
	"testing"
	"time"
)

func TestSlow(t *testing.T) {
	time.Sleep(5 * time.Second)
}
`
	if err := os.WriteFile(filepath.Join(root, "slow", "slow_test.go"), []byte(testSource), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write slow test: %v", err))
	}

	run := RunVerificationCommand(context.Background(), root, plancontract.VerificationCommand{
		Command: "go test ./slow",
		Argv:    []string{"go", "test", "./slow"},
	}, 200*time.Millisecond, false)
	check := run.Check
	gomega.Expect(check.Status != "failed").To(gomega.BeFalse(), fmt.Sprintf("status = %q, want failed", check.Status))
	gomega.Expect(strings.Contains(check.Error, "timed out")).To(gomega.BeTrue(), fmt.Sprintf("error = %q, want timeout", check.Error))
	gomega.Expect(run.Check.Error).To(gomega.ContainSubstring("timed out"), fmt.Sprintf("timeout run metadata = %#v", run))
	gomega.Expect(run.ExitCode).To(gomega.Equal(-1), fmt.Sprintf("timeout run metadata = %#v", run))
	gomega.Expect(run.StartedAt.IsZero()).To(gomega.BeFalse(), fmt.Sprintf("timeout run metadata = %#v", run))
	gomega.Expect(run.CompletedAt.IsZero()).To(gomega.BeFalse(), fmt.Sprintf("timeout run metadata = %#v", run))
})

var _ = ginkgo.It("run plan execute verification command uses working dir", func() {
	root := ginkgo.GinkgoT().TempDir()
	writePlanExecuteVerificationModule(root, "workingdir")
	writePlanExecuteVerificationFile(root, "pkg/pkg_test.go", `package pkg

import "testing"

func TestPass(t *testing.T) {}
`)

	run := RunVerificationCommand(context.Background(), root, plancontract.VerificationCommand{
		Command:    "go test -count=1 .",
		WorkingDir: "pkg",
	}, 10*time.Second, false)
	check := run.Check
	gomega.Expect(check.Status != plancontract.PhaseEvidenceStatusOK).To(gomega.BeFalse(), fmt.Sprintf("status = %q, want ok; check=%#v", check.Status, check))
	gomega.Expect(check.WorkingDir != "pkg").To(gomega.BeFalse(), fmt.Sprintf("WorkingDir = %q, want pkg", check.WorkingDir))
	gomega.Expect(strings.Contains(check.Summary, "ok")).To(gomega.BeTrue(), fmt.Sprintf("summary should contain go test output, got %q", check.Summary))
	gomega.Expect(run.ExitCode).To(gomega.Equal(0), fmt.Sprintf("pass run metadata = %#v", run))
	gomega.Expect(run.StartedAt.IsZero()).To(gomega.BeFalse(), fmt.Sprintf("pass run metadata = %#v", run))
	gomega.Expect(run.CompletedAt.IsZero()).To(gomega.BeFalse(), fmt.Sprintf("pass run metadata = %#v", run))
})

var _ = ginkgo.It("run plan execute verification command rejects unsafe working dir", func() {
	root := ginkgo.GinkgoT().TempDir()
	writePlanExecuteVerificationModule(root, "unsafeworkdir")

	run := RunVerificationCommand(context.Background(), root, plancontract.VerificationCommand{
		Command:    "go test ./...",
		WorkingDir: "../escape",
	}, 10*time.Second, false)
	check := run.Check
	gomega.Expect(check.Status != plancontract.PhaseEvidenceStatusFailed).To(gomega.BeFalse(), fmt.Sprintf("status = %q, want failed", check.Status))
	gomega.Expect(check.Error != "unsafe verification working_dir").To(gomega.BeFalse(), fmt.Sprintf("error = %q, want unsafe verification working_dir", check.Error))
	gomega.Expect(run.Check.Error).To(gomega.Equal("unsafe verification working_dir"), fmt.Sprintf("unsafe working dir metadata = %#v", run))
	gomega.Expect(run.ExitCode).To(gomega.Equal(-1), fmt.Sprintf("unsafe working dir metadata = %#v", run))
})

var _ = ginkgo.It("run plan execute verification command failure uses combined output", func() {
	root := ginkgo.GinkgoT().TempDir()
	writePlanExecuteVerificationModule(root, "failureoutput")
	writePlanExecuteVerificationFile(root, "pkg/pkg_test.go", `package pkg

import (
	"fmt"
	"os"
	"testing"
)

func TestFail(t *testing.T) {
	fmt.Println("STDOUT_MARKER")
	fmt.Fprintln(os.Stderr, "STDERR_MARKER")
	t.Fatal("FAIL_MARKER")
}
`)

	run := RunVerificationCommand(context.Background(), root, plancontract.VerificationCommand{
		Command: "go test -count=1 ./pkg",
	}, 10*time.Second, false)
	check := run.Check
	gomega.Expect(check.Status != plancontract.PhaseEvidenceStatusFailed).To(gomega.BeFalse(), fmt.Sprintf("status = %q, want failed", check.Status))

	for _, want := range []string{"STDOUT_MARKER", "STDERR_MARKER", "FAIL_MARKER"} {
		gomega.Expect(strings.Contains(check.Error, want)).To(gomega.BeTrue(), fmt.Sprintf("error should include %q from combined output, got %q", want, check.Error))

	}
	gomega.Expect(strings.Contains(check.Summary, "exit status")).To(gomega.BeTrue(), fmt.Sprintf("summary should preserve process error, got %q", check.Summary))
	gomega.Expect(run.ExitCode).NotTo(gomega.Equal(0), fmt.Sprintf("failure run metadata = %#v", run))
	gomega.Expect(run.StartedAt.IsZero()).To(gomega.BeFalse(), fmt.Sprintf("failure run metadata = %#v", run))
	gomega.Expect(run.CompletedAt.IsZero()).To(gomega.BeFalse(), fmt.Sprintf("failure run metadata = %#v", run))
})

var _ = ginkgo.It("run plan execute verification command truncates output", func() {
	root := ginkgo.GinkgoT().TempDir()
	writePlanExecuteVerificationModule(root, "truncateverify")
	writePlanExecuteVerificationFile(root, "pkg/pkg_test.go", `package pkg

import (
	"strings"
	"testing"
)

func TestChatty(t *testing.T) {
	t.Log(strings.Repeat("x", 13000))
}
`)

	check := RunVerificationCommand(context.Background(), root, plancontract.VerificationCommand{
		Command: "go test -count=1 -v ./pkg",
	}, 10*time.Second, false).Check
	gomega.Expect(check.Status != plancontract.PhaseEvidenceStatusOK).To(gomega.BeFalse(), fmt.Sprintf("status = %q, want ok; check=%#v", check.Status, check))
	gomega.Expect(strings.Contains(check.Summary, "[truncated")).To(gomega.BeTrue(), fmt.Sprintf("summary should include truncation marker, len=%d summary tail=%q", len(check.Summary), check.Summary[max(0, len(check.Summary)-80):]))
})

var _ = ginkgo.It("run verification command can require operand after prefix", func() {
	root := ginkgo.GinkgoT().TempDir()
	writePlanExecuteVerificationModule(root, "requireoperand")

	run := RunVerificationCommand(context.Background(), root, plancontract.VerificationCommand{
		Command: "go test",
	}, 10*time.Second, true)
	gomega.Expect(run.Check.Status).To(gomega.Equal(plancontract.PhaseEvidenceStatusFailed), fmt.Sprintf("bare prefix should be unsupported, got %#v", run))
	gomega.Expect(run.ExitCode).To(gomega.Equal(-1), fmt.Sprintf("bare prefix should be unsupported, got %#v", run))
	gomega.Expect(strings.Contains(run.Check.Error, "package pattern")).To(gomega.BeTrue(), fmt.Sprintf("error = %q, want package pattern", run.Check.Error))
})

func writePlanExecuteVerificationModule(root string, module string) {
	ginkgo.GinkgoHelper()

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write go.mod: %v", err))
	}
}

func writePlanExecuteVerificationFile(root string, rel string, body string) {
	ginkgo.GinkgoHelper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir %s: %v", filepath.Dir(path), err))
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write %s: %v", rel, err))
	}
}

func decodeSinglePlanExecuteEventForTest(output string) map[string]any {
	ginkgo.GinkgoHelper()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	gomega.Expect(len(lines) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one event line, got %d: %q", len(lines), output))

	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode event: %v", err))
	}

	return event
}
