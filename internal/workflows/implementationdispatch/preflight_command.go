package implementationdispatch

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/platform/textutil"
)

func AtomIntakeBlocker(cmd PreflightCommandResult) (string, []string) {
	type atomReport struct {
		State        string   `json:"state"`
		Dispatchable bool     `json:"dispatchable"`
		Reasons      []string `json:"reasons"`
		NextActions  []string `json:"next_actions"`
		Dirty        []string `json:"dirty_paths"`
		Invalid      []string `json:"invalid_paths"`
	}

	var report atomReport

	if err := json.Unmarshal([]byte(cmd.Stdout), &report); err == nil && strings.TrimSpace(report.State) != "" {
		state := strings.TrimSpace(report.State)

		var parts []string

		parts = append(parts, "atom intake state="+state)

		if len(report.Reasons) > 0 {
			parts = append(parts, strings.Join(report.Reasons, "; "))
		}

		if len(report.Dirty) > 0 {
			parts = append(parts, "dirty paths: "+strings.Join(report.Dirty, ", "))
		}

		if len(report.Invalid) > 0 {
			parts = append(parts, "invalid paths: "+strings.Join(report.Invalid, ", "))
		}

		if len(report.NextActions) > 0 {
			parts = append(parts, "next: "+strings.Join(report.NextActions, "; "))
		}

		return state, []string{strings.Join(parts, " - ")}
	}

	return "", []string{"atom intake failed: " + compactCommandOutput(cmd)}
}

type briefLintReport struct {
	Exit     int `json:"exit"`
	Findings []struct {
		Severity string `json:"severity"`
		Category string `json:"category"`
		Message  string `json:"message"`
	} `json:"findings"`
}

func BriefLintBlockers(cmd PreflightCommandResult) []string {
	report, ok := parseBriefLintReport(cmd)
	if !ok {
		return []string{"brief lint failed: " + compactCommandOutput(cmd)}
	}

	var blockers []string

	for _, finding := range report.Findings {
		if textutil.EqualTrimFold(finding.Severity, "WARN") || textutil.EqualTrimFold(finding.Severity, "INFO") {
			continue
		}

		message := strings.TrimSpace(finding.Message)
		if message == "" {
			continue
		}

		blockers = append(blockers, "brief lint: "+message)
		if len(blockers) >= 4 {
			break
		}
	}

	if len(blockers) == 0 {
		blockers = append(blockers, fmt.Sprintf("brief lint failed with exit %d", report.Exit))
	}

	return blockers
}

func BriefLintWarnings(cmd PreflightCommandResult) []string {
	report, ok := parseBriefLintReport(cmd)
	if !ok {
		return nil
	}

	var warnings []string

	for _, finding := range report.Findings {
		if !textutil.EqualTrimFold(finding.Severity, "WARN") {
			continue
		}

		message := strings.TrimSpace(finding.Message)
		if message == "" {
			continue
		}

		warnings = append(warnings, "brief lint: "+message)
		if len(warnings) >= 3 {
			break
		}
	}

	return warnings
}

func parseBriefLintReport(cmd PreflightCommandResult) (briefLintReport, bool) {
	var report briefLintReport

	if err := json.Unmarshal([]byte(cmd.Stdout), &report); err != nil {
		return report, false
	}

	return report, true
}

func compactCommandOutput(cmd PreflightCommandResult) string {
	parts := []string{}
	if cmd.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit=%d", cmd.ExitCode))
	}

	if cmd.Err != nil {
		parts = append(parts, cmd.Err.Error())
	}

	combined := strings.TrimSpace(cmd.Stdout + "\n" + cmd.Stderr)
	if combined != "" {
		parts = append(parts, compactWhitespace(combined))
	}

	if len(parts) == 0 {
		return "no diagnostic output"
	}

	return strings.Join(parts, "; ")
}
