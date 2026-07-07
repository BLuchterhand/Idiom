package implementationdispatch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBriefLintBlockersDoNotTruncateActionableLine(t *testing.T) {
	longBlocker := "exact-anchor specificity failed for required edit path(s): " + strings.Repeat("internal/pkg/path.go ", 40) + "Each required edit path must carry at least one `<path>:<line>` reference."

	payload, err := json.Marshal(map[string]any{
		"exit": 1,
		"findings": []map[string]string{
			{
				"severity": "BLOCK",
				"category": "anchor-specificity",
				"message":  longBlocker,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal lint report: %v", err)
	}

	blockers := BriefLintBlockers(PreflightCommandResult{
		ExitCode: 1,
		Stdout:   string(payload),
	})
	joined := strings.Join(blockers, "\n")

	if !strings.Contains(joined, longBlocker) {
		t.Fatalf("lint blockers lost actionable blocker:\n%s", joined)
	}

	if strings.Contains(joined, "[truncated") {
		t.Fatalf("lint blockers should not include truncation marker:\n%s", joined)
	}
}
