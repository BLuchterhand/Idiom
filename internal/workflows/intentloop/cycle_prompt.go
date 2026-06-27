package intentloop

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	intentloopcontract "idiom/internal/contracts/intentloop"
)

const maxPromptGapDetailRunes = 2000

func BuildCyclePrompt(originalPrompt string, state IntentLoopState) string {
	var b strings.Builder
	b.WriteString("## Original Intent\n\n")
	b.WriteString(strings.TrimRight(originalPrompt, "\n"))
	b.WriteString("\n\n")
	b.WriteString(renderPriorCycles(state.Cycles))
	b.WriteString("\n\n")
	b.WriteString(renderRemainingRequirements(state.Cycles))
	b.WriteString("\n\n")
	b.WriteString("## Instructions\n\n")
	b.WriteString("Produce a plan (submit_plan) that satisfies the remaining contract requirements listed above. ")
	b.WriteString("Edits from prior cycles already cover part of the contract; target only the remaining gaps.\n\n")
	b.WriteString("The harness will reject any planned edit that lacks current source-body evidence for the file being edited. ")
	b.WriteString("Prior-cycle summaries describe what changed but are not a substitute for source-body evidence.\n\n")
	b.WriteString("Source-satisfied from a prior cycle does not satisfy remaining contract requirements. ")
	b.WriteString("If the remaining requirements cannot be resolved with the current scope contract, report a blocker with `<<QUESTION>>:` and the exact obstacle.\n")
	return b.String()
}

func SaveCyclePrompt(root string, sessionID string, cycleNumber int, prompt string) (string, string, error) {
	if strings.TrimSpace(sessionID) == "" || !validIntentLoopSessionID.MatchString(strings.TrimSpace(sessionID)) {
		return "", "", fmt.Errorf("invalid intent loop session id %q", sessionID)
	}
	if cycleNumber <= 0 {
		return "", "", fmt.Errorf("invalid intent loop cycle number %d", cycleNumber)
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}

	relPath := filepath.ToSlash(filepath.Join(".idiom", "intent-loop", "sessions", strings.TrimSpace(sessionID), fmt.Sprintf("cycle-%03d.prompt.md", cycleNumber)))
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir cycle prompt dir: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(prompt), 0o600); err != nil {
		return "", "", fmt.Errorf("write cycle prompt: %w", err)
	}
	return relPath, sha256Hex(prompt), nil
}

func LoadCyclePrompt(root string, relPath string, expectedSHA256 string) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" || filepath.IsAbs(relPath) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(relPath)), "../") {
		return "", fmt.Errorf("invalid cycle prompt path %q", relPath)
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return "", fmt.Errorf("read cycle prompt %s: %w", relPath, err)
	}
	prompt := string(body)
	if expectedSHA256 != "" && sha256Hex(prompt) != expectedSHA256 {
		return "", fmt.Errorf("cycle prompt hash mismatch for %s", relPath)
	}
	return prompt, nil
}

func renderPriorCycles(cycles []CycleRecord) string {
	completed := completedCycles(cycles)
	if len(completed) == 0 {
		return "## Prior Cycles\n\nNo prior cycles completed."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Prior Cycles\n\n%d prior cycles completed.\n\n", len(completed))
	changedPaths := cumulativeChangedPaths(completed)
	if len(changedPaths) > 0 {
		b.WriteString("Cumulative changed paths: ")
		for i, path := range changedPaths {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "`%s`", path)
		}
		b.WriteString("\n\n")
	}

	b.WriteString("### Previously passing evidence\n")
	writePassingEvidence(&b, completed)
	b.WriteString("\n### Previously failing evidence\n")
	writeFailingEvidence(&b, completed)
	return strings.TrimRight(b.String(), "\n")
}

func renderRemainingRequirements(cycles []CycleRecord) string {
	var latest *CycleRecord
	for i := len(cycles) - 1; i >= 0; i-- {
		if len(cycles[i].RemainingFailures) > 0 {
			latest = &cycles[i]
			break
		}
	}
	if latest == nil {
		return "## Remaining Requirements\n\nNo remaining contract requirements are recorded yet."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Remaining Requirements\n\nThe following contract requirements are still unmet after %d cycles:\n", latest.CycleNumber)
	for _, gap := range latest.RemainingFailures {
		fmt.Fprintf(&b, "\n- `%s` — %s", gap.Requirement, gap.Status)
		if gap.Path != "" {
			fmt.Fprintf(&b, " (`%s`)", gap.Path)
		}
		detail := truncateRunes(strings.TrimSpace(gap.Detail), maxPromptGapDetailRunes)
		if detail != "" {
			fmt.Fprintf(&b, "\n  ```\n  %s\n  ```", indentBlock(detail, "  "))
		}
		if gap.DetailHash != "" {
			fmt.Fprintf(&b, "\n  detail_hash: `%s`", gap.DetailHash)
		}
		if gap.ThresholdResult != "" {
			fmt.Fprintf(&b, "\n  threshold_result: `%s`", gap.ThresholdResult)
		}
	}
	return b.String()
}

func completedCycles(cycles []CycleRecord) []CycleRecord {
	var out []CycleRecord
	for _, cycle := range cycles {
		if cycle.Status == intentloopcontract.CycleStatusCompleted || cycle.Status == intentloopcontract.CycleStatusFailed {
			out = append(out, cycle)
		}
	}
	return out
}

func cumulativeChangedPaths(cycles []CycleRecord) []string {
	var paths []string
	for _, cycle := range cycles {
		paths = append(paths, cycle.ChangedPaths...)
	}
	return uniqueStrings(paths)
}

func writePassingEvidence(b *strings.Builder, cycles []CycleRecord) {
	var lines []string
	if cycleList := cyclesWith(cycles, func(c CycleRecord) bool { return c.SourceSanityPassed }); len(cycleList) > 0 {
		lines = append(lines, "- Source sanity: passed ("+cycleList+")")
	}
	if cycleList := cyclesWith(cycles, func(c CycleRecord) bool { return c.VerificationPassed }); len(cycleList) > 0 {
		lines = append(lines, "- Verification: passed ("+cycleList+")")
	}
	if cycleList := cyclesWith(cycles, func(c CycleRecord) bool { return c.ReviewPassed }); len(cycleList) > 0 {
		lines = append(lines, "- Review: passed ("+cycleList+")")
	}
	if len(lines) == 0 {
		b.WriteString("- None recorded\n")
		return
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func writeFailingEvidence(b *strings.Builder, cycles []CycleRecord) {
	var lines []string
	for _, cycle := range cycles {
		for _, gap := range cycle.RemainingFailures {
			detail := truncateRunes(strings.TrimSpace(gap.Detail), 240)
			line := fmt.Sprintf("- `%s`: %s in cycle %d", gap.Requirement, strings.ToUpper(gap.Status), cycle.CycleNumber)
			if detail != "" {
				line += ": " + strings.Join(strings.Fields(detail), " ")
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		b.WriteString("- None recorded\n")
		return
	}
	sort.Strings(lines)
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func cyclesWith(cycles []CycleRecord, predicate func(CycleRecord) bool) string {
	var ids []string
	for _, cycle := range cycles {
		if predicate(cycle) {
			ids = append(ids, fmt.Sprintf("cycle %d", cycle.CycleNumber))
		}
	}
	return strings.Join(ids, ", ")
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func indentBlock(value string, prefix string) string {
	lines := strings.Split(value, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
