package plancontract

import (
	"strings"
	"testing"
)

func TestReviewRepairAnswerDeclaresPassRequiresTerminalShape(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "decision first",
			text: "Decision: pass\n\nVerification Checklist:\n- ok",
			want: true,
		},
		{
			name: "checklist first",
			text: "Verification Checklist:\n- ok\n\nDecision: pass",
			want: true,
		},
		{
			name: "template prose before decision",
			text: "Your visible response must use exactly one of these shapes.\nDecision: pass\n\nVerification Checklist:\n- ok",
			want: false,
		},
		{
			name: "embedded decision",
			text: "I will answer Decision: pass after review.\nVerification Checklist:\n- ok",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReviewRepairAnswerDeclaresPass(tc.text); got != tc.want {
				t.Fatalf("ReviewRepairAnswerDeclaresPass() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReviewRepairTextHasChecklist(t *testing.T) {
	if !ReviewRepairTextHasChecklist("Decision: pass\n\nVerification Checklist:\n- ok") {
		t.Fatalf("expected checklist heading to be recognized")
	}
	if !ReviewRepairTextHasChecklist("Verification Checklist: - checked fact") {
		t.Fatalf("expected serialized checklist prefix to be recognized")
	}
	if ReviewRepairTextHasChecklist("Checklist:\n- ok") {
		t.Fatalf("generic checklist heading must not satisfy review-repair contract")
	}
}

func TestReviewRepairTerminalTemplatesStayParserReadable(t *testing.T) {
	templates := ReviewRepairTerminalTemplates()
	for _, required := range []string{
		"Decision: pass",
		"Verification Checklist:",
		"parser-readable submit_plan object",
		"<<QUESTION>>:",
	} {
		if !strings.Contains(templates, required) {
			t.Fatalf("terminal templates missing %q:\n%s", required, templates)
		}
	}
	if strings.Contains(templates, "cite a checked fact") {
		t.Fatalf("terminal templates must not expose copyable checklist placeholders:\n%s", templates)
	}
}

func TestReviewRepairLineIsPlaceholderChecklist(t *testing.T) {
	cases := []string{
		"- Required behavior: cite a checked fact from Phase/Diff Evidence.",
		"- Scope safety: cite a checked fact from changed paths and allowed repair paths.",
		"- Test or command evidence: cite a checked fact, or say it is pending outside this lane.",
	}
	for _, tc := range cases {
		if !ReviewRepairLineIsPlaceholderChecklist(tc) {
			t.Fatalf("expected placeholder checklist line to be detected: %q", tc)
		}
	}
	if ReviewRepairLineIsPlaceholderChecklist("- Required behavior: src/app.go now returns the requested label in line 12.") {
		t.Fatalf("concrete checklist fact must not be treated as placeholder text")
	}
}
