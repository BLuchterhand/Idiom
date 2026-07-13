package eval

import (
	"encoding/json"
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("requires lifecycle records in order while allowing unrelated records", func() {
	const (
		pathA = "internal/labels/labels.go"
		pathB = "internal/labels/status.go"
	)

	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Update both labels."}},
		{Type: "tool_request", Data: map[string]any{"tool": "edit_file", "raw": `{"path":"` + pathA + `"}`}},
		{Type: "run_event", Data: map[string]any{"role": "tool_policy_result", "content": "write allowed"}},
		{Type: "tool_result", Data: map[string]any{"tool": "edit_file", "summary": "Edited " + pathA, "error": ""}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "edit_file",
			"summary": "Tool not available in current runtime contract. Rejected write target: " + pathB,
			"error":   "edit_file is not in the current allowed tool surface",
		}},
		{Type: "run_event", Data: map[string]any{"role": "post_write_verification_required", "content": "verify " + pathA}},
		{Type: "tool_result", Data: map[string]any{"tool": "source_sanity", "summary": "Source sanity passed for " + pathA, "error": ""}},
		{Type: "tool_request", Data: map[string]any{"tool": "edit_file", "action": "edit_file:" + pathB}},
		{Type: "tool_result", Data: map[string]any{"tool": "edit_file", "summary": "Edited " + pathB, "error": ""}},
		{Type: "phase", Data: map[string]any{"lane": "implement", "phase": "verify", "state": "done", "detail": "Verification passed"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Updated and verified both files."}},
	}

	report := Evaluate("ordered.jsonl", records, Scenario{
		Name: "rejected-second-write-recovery",
		RequireOrderedEvents: []OrderedEventExpectation{
			{RecordType: "tool_request", Tool: "edit_file", Path: pathA, Outcome: "requested"},
			{RecordType: "tool_result", Tool: "edit_file", Path: pathA, Outcome: "success", Contains: []string{"Edited"}},
			{RecordType: "tool_result", Tool: "edit_file", Path: pathB, Outcome: "rejected", Contains: []string{"allowed tool surface"}},
			{RecordType: "run_event", Role: "post_write_verification_required", Path: pathA},
			{RecordType: "tool_result", Tool: "source_sanity", Path: pathA, Outcome: "succeeded"},
			{RecordType: "tool_request", Tool: "edit_file", Path: pathB},
			{RecordType: "tool_result", Tool: "edit_file", Path: pathB, Outcome: "ok"},
			{RecordType: "phase", Outcome: "done", Contains: []string{"Verification passed"}},
			{RecordType: "message", Role: "assistant", Contains: []string{"both files"}},
		},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("ordered lifecycle should pass, findings=%#v", report.Findings))
})

var _ = ginkgo.It("rejects lifecycle records that exist only out of order", func() {
	records := []Record{
		{Type: "tool_result", Data: map[string]any{"tool": "edit_file", "path": "b.go", "summary": "Edit rejected", "error": "not in the current allowed tool surface"}},
		{Type: "tool_result", Data: map[string]any{"tool": "source_sanity", "path": "a.go", "summary": "Source sanity passed", "error": ""}},
		{Type: "tool_result", Data: map[string]any{"tool": "edit_file", "path": "b.go", "summary": "Edited b.go", "error": ""}},
	}

	report := Evaluate("out-of-order.jsonl", records, Scenario{
		Name: "out-of-order",
		RequireOrderedEvents: []OrderedEventExpectation{
			{RecordType: "tool_result", Tool: "source_sanity", Path: "a.go", Outcome: "success"},
			{RecordType: "tool_result", Tool: "edit_file", Path: "b.go", Outcome: "rejected"},
			{RecordType: "tool_result", Tool: "edit_file", Path: "b.go", Outcome: "success"},
		},
	})

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("out-of-order lifecycle unexpectedly passed: %#v", report))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("required ordered event 2 was not observed after the prior match"), fmt.Sprintf("unexpected findings: %#v", report.Findings))
})

var _ = ginkgo.It("matches typed top-level event outcomes", func() {
	records := []Record{
		{Type: "idiom.feature_planner.atom_source_satisfied", Data: map[string]any{
			"path":    "internal/labels/labels.go",
			"outcome": "source_satisfied",
			"reason":  "implementation already matches the requested behavior",
		}},
	}

	report := Evaluate("typed-outcome.jsonl", records, Scenario{
		Name: "typed-outcome",
		RequireOrderedEvents: []OrderedEventExpectation{{
			RecordType: "idiom.feature_planner.atom_source_satisfied",
			Path:       "internal/labels/labels.go",
			Outcome:    "source_satisfied",
			Contains:   []string{"already matches"},
		}},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("typed outcome should pass, findings=%#v", report.Findings))
})

var _ = ginkgo.It("matches typed tool rejection outcomes without prose classification", func() {
	records := []Record{{Type: "tool_result", Data: map[string]any{
		"tool":                  "edit_file",
		"summary":               "Mutation could not be performed",
		"error":                 "scope mismatch",
		"rejection_disposition": "policy_denied",
		"rejected_write_paths":  []any{"internal/labels/labels.go"},
	}}}

	report := Evaluate("typed-tool-rejection.jsonl", records, Scenario{
		Name: "typed-tool-rejection",
		RequireOrderedEvents: []OrderedEventExpectation{{
			RecordType: "tool_result",
			Tool:       "edit_file",
			Path:       "internal/labels/labels.go",
			Outcome:    "rejected",
		}},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("typed rejection outcome should pass, findings=%#v", report.Findings))
})

var _ = ginkgo.It("rejects empty ordered event expectations in evaluation and suite lint", func() {
	scenario := Scenario{
		Name:                 "empty-ordered-event",
		RequireOrderedEvents: []OrderedEventExpectation{{Contains: []string{"  "}}},
	}
	report := Evaluate("empty.jsonl", nil, scenario)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("empty expectation unexpectedly passed: %#v", report))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("required ordered event 1 has no match criteria"), fmt.Sprintf("unexpected findings: %#v", report.Findings))

	suite := SWESuite{
		Name: "ordered-event-lint",
		Tasks: []SWETask{{
			ID:       "empty-ordered-event",
			Family:   "implementation",
			Prompt:   "Update the file.",
			Scenario: scenario,
		}},
	}
	gomega.Expect(strings.Join(LintSWESuite(suite), "\n")).To(gomega.ContainSubstring("require_ordered_events[0] must match"), "suite lint should reject empty ordered event expectations")
})

var _ = ginkgo.It("round trips ordered event expectations through scenario json", func() {
	scenario := Scenario{
		Name: "ordered-round-trip",
		RequireOrderedEvents: []OrderedEventExpectation{{
			RecordType: "tool_result",
			Tool:       "edit_file",
			Path:       "internal/labels/status.go",
			Outcome:    "rejected",
			Contains:   []string{"allowed tool surface"},
		}},
	}

	data, err := json.Marshal(scenario)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal scenario: %v", err))

	var decoded Scenario
	gomega.Expect(json.Unmarshal(data, &decoded)).To(gomega.Succeed(), "unmarshal scenario")
	gomega.Expect(decoded.RequireOrderedEvents).To(gomega.Equal(scenario.RequireOrderedEvents), fmt.Sprintf("ordered event round trip mismatch: %s", data))
})
