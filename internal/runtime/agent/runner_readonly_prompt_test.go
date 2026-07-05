package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
)

func TestReadOnlyInvestigationStrategyPromptOnlyMentionsVisibleDiscoveryTools(t *testing.T) {
	locateTools := []string{
		workspace.ToolNamePwd,
		workspace.ToolNameSearchFiles,
		workspace.ToolNameSearchContent,
		workspace.ToolNameReadFile,
		workspace.ToolNameCodeNavigate,
		workspace.ToolNameContextQuery,
		workspace.ToolNameContextZoom,
	}

	prompt := readOnlyInvestigationStrategyPrompt(locateTools)
	for _, forbidden := range []string{workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("locate/read-only prompt mentioned hidden tool %q:\n%s", forbidden, prompt)
		}
	}

	for _, want := range []string{workspace.ToolNameContextQuery, workspace.ToolNameSearchContent} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("locate/read-only prompt should mention visible strategy tool %q:\n%s", want, prompt)
		}
	}

	broadTools := append(append([]string(nil), locateTools...), workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery)
	broadPrompt := readOnlyInvestigationStrategyPrompt(broadTools)
	for _, want := range []string{workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery} {
		if !strings.Contains(broadPrompt, want) {
			t.Fatalf("broad read-only prompt should keep visible discovery tool %q:\n%s", want, broadPrompt)
		}
	}
}

func TestNarrowToolFamilyRepetitionNudgeOnlyMentionsVisibleDiscoveryTools(t *testing.T) {
	generalTools := []string{
		workspace.ToolNameSearchFiles,
		workspace.ToolNameSearchContent,
		workspace.ToolNameReadFile,
		workspace.ToolNameCodeNavigate,
		workspace.ToolNameReadOnlyCapability,
		workspace.ToolNameHarnessAudit,
		workspace.ToolNameContextStatus,
		workspace.ToolNameContextQuery,
		workspace.ToolNameContextZoom,
	}

	prompt := narrowToolFamilyRepetitionNudgePrompt("code_navigate", narrowToolFamilyNudgeAt, generalTools)
	for _, forbidden := range []string{workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("general read-only nudge mentioned hidden tool %q:\n%s", forbidden, prompt)
		}
	}

	for _, want := range []string{workspace.ToolNameContextQuery, workspace.ToolNameHarnessAudit, workspace.ToolNameSearchContent} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("general read-only nudge should mention visible strategy tool %q:\n%s", want, prompt)
		}
	}

	broadTools := append(append([]string(nil), generalTools...), workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery)
	broadPrompt := narrowToolFamilyRepetitionNudgePrompt("code_navigate", narrowToolFamilyNudgeAt, broadTools)
	for _, want := range []string{workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery} {
		if !strings.Contains(broadPrompt, want) {
			t.Fatalf("broad read-only nudge should keep visible discovery tool %q:\n%s", want, broadPrompt)
		}
	}
}
